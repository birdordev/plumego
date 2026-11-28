package delivery

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	dbutil "github.com/birdordev/plumego/store/db"

	"with-cloud-manager/migrations"
)

var ErrReceiptManifest = errors.New("invalid terminal receipt manifest")
var ErrReconcileWindowTooLarge = errors.New("terminal receipt reconciliation window has more than 1000 missing effects")

const MaxReconcileReceipts = 1000

type ReceiptManifest struct {
	Sink     string            `json:"sink"`
	Tenant   string            `json:"tenant"`
	Project  string            `json:"project"`
	Coverage *ReceiptCoverage  `json:"coverage,omitempty"`
	Receipts []TerminalReceipt `json:"receipts"`
}

type ReceiptCoverage struct {
	From  time.Time `json:"from"`
	Until time.Time `json:"until"`
}

type TerminalReceipt struct {
	DeliveryID  string     `json:"delivery_id"`
	OperationID string     `json:"operation_id"`
	EffectID    string     `json:"effect_id"`
	BodySHA256  string     `json:"body_sha256"`
	Type        string     `json:"type"`
	Action      string     `json:"action"`
	Account     string     `json:"account"`
	Region      string     `json:"region,omitempty"`
	Asset       string     `json:"asset_id,omitempty"`
	Volume      string     `json:"volume_id,omitempty"`
	OccurredAt  *time.Time `json:"occurred_at,omitempty"`
}

type Reconciliation struct {
	Sink      string               `json:"sink"`
	Tenant    string               `json:"tenant"`
	Project   string               `json:"project"`
	CheckedAt time.Time            `json:"checked_at"`
	Coverage  *ReceiptCoverage     `json:"coverage,omitempty"`
	Clean     bool                 `json:"clean"`
	Counts    map[string]int       `json:"counts"`
	Items     []ReconciliationItem `json:"items"`
}

type ReconciliationItem struct {
	EffectID            string     `json:"effect_id"`
	OperationID         string     `json:"operation_id"`
	DeliveryID          string     `json:"delivery_id,omitempty"`
	State               string     `json:"state"`
	SourceStatus        string     `json:"source_status,omitempty"`
	SourceDelivery      string     `json:"source_delivery_id,omitempty"`
	SourceDeliveryState string     `json:"source_delivery_state,omitempty"`
	SourceOccurredAt    *time.Time `json:"source_occurred_at,omitempty"`
}

func (m ReceiptManifest) validate(dest Destination) error {
	if m.Sink != dest.id || m.Tenant != dest.tenant || m.Project != dest.project {
		return ErrBinding
	}
	if len(m.Receipts) > MaxReconcileReceipts || (m.Coverage == nil && len(m.Receipts) == 0) {
		return ErrReceiptManifest
	}
	if m.Coverage != nil && (m.Receipts == nil || m.Coverage.From.IsZero() ||
		!m.Coverage.From.Before(m.Coverage.Until)) {
		return ErrReceiptManifest
	}
	seen := make(map[string]struct{}, len(m.Receipts))
	for _, r := range m.Receipts {
		kind, status, cut := strings.Cut(r.Type, ".operation.")
		hash, err := hex.DecodeString(r.BodySHA256)
		if !publicID.MatchString(r.DeliveryID) || !identifier.MatchString(r.OperationID) ||
			r.EffectID != "cmp-op-final-v1:"+r.OperationID || err != nil || len(hash) != sha256.Size ||
			hex.EncodeToString(hash) != r.BodySHA256 || !cut || !identifier.MatchString(kind) ||
			!finalOutcome(status) || !identifier.MatchString(r.Action) || !identifier.MatchString(r.Account) ||
			!validReceiptLabel(r.Region) || !validReceiptLabel(r.Asset) || !validReceiptLabel(r.Volume) {
			return ErrReceiptManifest
		}
		if _, duplicate := seen[r.DeliveryID]; duplicate {
			return ErrReceiptManifest
		}
		if m.Coverage != nil && (r.OccurredAt == nil || r.OccurredAt.Before(m.Coverage.From) ||
			!r.OccurredAt.Before(m.Coverage.Until)) {
			return ErrReceiptManifest
		}
		seen[r.DeliveryID] = struct{}{}
	}
	return nil
}

func finalOutcome(status string) bool { return status == "succeeded" || status == "failed" }

func validReceiptLabel(value string) bool {
	return len(value) <= 512 && utf8.ValidString(value) && strings.IndexFunc(value, unicode.IsControl) < 0
}

// ReconcileTerminalReceipts 对账已验签并由接收方持久提交的回执，只读查询一个绑定目的地。
func (s Store) ReconcileTerminalReceipts(ctx context.Context, dest Destination, manifest ReceiptManifest) (Reconciliation, error) {
	if err := manifest.validate(dest); err != nil {
		return Reconciliation{}, err
	}
	input, err := json.Marshal(manifest.Receipts)
	if err != nil {
		return Reconciliation{}, err
	}
	result := Reconciliation{Sink: dest.id, Tenant: dest.tenant, Project: dest.project, Clean: true,
		Counts: make(map[string]int), Items: make([]ReconciliationItem, 0, len(manifest.Receipts))}
	err = dbutil.WithTransaction(ctx, s.db, &sql.TxOptions{ReadOnly: true, Isolation: sql.LevelRepeatableRead}, func(tx *sql.Tx) error {
		if err := checkDestinationBinding(ctx, tx, dest); err != nil {
			return err
		}
		if err := tx.QueryRowContext(ctx, "SELECT transaction_timestamp()").Scan(&result.CheckedAt); err != nil {
			return err
		}
		result.CheckedAt = result.CheckedAt.UTC()
		if manifest.Coverage != nil {
			if manifest.Coverage.Until.After(result.CheckedAt) {
				return ErrReceiptManifest
			}
			result.Coverage = &ReceiptCoverage{From: manifest.Coverage.From.UTC(), Until: manifest.Coverage.Until.UTC()}
		}
		if err := result.reconcileProvided(ctx, tx, dest, manifest.Receipts, input); err != nil {
			return err
		}
		if result.Coverage != nil {
			if err := result.reconcileMissing(ctx, tx, dest, input); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return Reconciliation{}, err
	}
	return result, nil
}

func (r *Reconciliation) reconcileProvided(ctx context.Context, tx *sql.Tx, dest Destination, receipts []TerminalReceipt, input []byte) (err error) {
	if len(receipts) == 0 {
		return nil
	}
	rows, err := tx.QueryContext(ctx, migrations.Reconcile(), dest.tenant, dest.project, dest.id, input)
	if err != nil {
		return err
	}
	defer func() {
		if closeErr := rows.Close(); closeErr != nil {
			err = errors.Join(err, closeErr)
		}
	}()
	for rows.Next() {
		var local receiptSource
		if err := rows.Scan(&local.status, &local.kind, &local.action, &local.account,
			&local.region, &local.asset, &local.volume, &local.delivery, &local.deliveryType,
			&local.deliveryOperation, &local.exactBody, &local.exactOccurredAt); err != nil {
			return err
		}
		if len(r.Items) == len(receipts) {
			return errors.New("unexpected terminal receipt reconciliation count")
		}
		receipt := receipts[len(r.Items)]
		state := local.classify(receipt)
		r.Items = append(r.Items, ReconciliationItem{
			EffectID: receipt.EffectID, OperationID: receipt.OperationID, DeliveryID: receipt.DeliveryID,
			State: state, SourceStatus: local.status, SourceDelivery: local.delivery,
		})
		r.Counts[state]++
		r.Clean = r.Clean && (state == "matched" || state == "regenerated")
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if len(r.Items) != len(receipts) {
		return errors.New("incomplete terminal receipt reconciliation")
	}
	return nil
}

type receiptSource struct {
	status, kind, action, account, region, asset, volume string
	delivery, deliveryType, deliveryOperation            string
	exactBody                                            []byte
	exactOccurredAt                                      sql.NullTime
}

func (s receiptSource) classify(receipt TerminalReceipt) string {
	kind, _, _ := strings.Cut(receipt.Type, ".operation.")
	switch {
	case s.status == "":
		return "source_missing"
	case (s.exactBody != nil && (s.deliveryOperation != receipt.OperationID || s.deliveryType != receipt.Type)) ||
		s.kind != kind || s.action != receipt.Action || s.account != receipt.Account || s.region != receipt.Region:
		return "conflict"
	case !finalOutcome(s.status):
		return "source_unsettled"
	case s.kind+".operation."+s.status != receipt.Type || s.asset != receipt.Asset || s.volume != receipt.Volume:
		return "conflict"
	case s.delivery == "":
		return "outbox_missing"
	case s.deliveryType != receipt.Type || s.deliveryOperation != receipt.OperationID:
		return "conflict"
	case s.delivery != receipt.DeliveryID:
		return "regenerated"
	}
	digest := sha256.Sum256(s.exactBody)
	if hex.EncodeToString(digest[:]) != receipt.BodySHA256 ||
		(receipt.OccurredAt != nil && (!s.exactOccurredAt.Valid || !s.exactOccurredAt.Time.Equal(*receipt.OccurredAt))) {
		return "conflict"
	}
	return "matched"
}
