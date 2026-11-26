package operations

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	_ "embed"
	"encoding/json"
	"errors"
	"time"

	"github.com/birdordev/plumego/x/messaging/mq"

	"with-cloud-manager/internal/identity"
	"with-cloud-manager/internal/inventory"
	"with-cloud-manager/internal/provider"
)

//go:embed 0074_dns_record_creation.sql
var dnsRecordCreationSchema string

const createDNSRecord = "create-dns-record"

type DNSRecordCreationInput struct {
	Account    string    `json:"account"`
	AssetID    string    `json:"asset_id"`
	ObservedAt time.Time `json:"observed_at"`
	provider.DNSRecordSpec
}

type DNSRecordCreationView struct {
	ZoneName string `json:"zone_name"`
	RecordID string `json:"record_id,omitempty"`
	provider.DNSRecordSpec
	NeedsRecordID bool `json:"needs_record_id,omitempty"`
}

type dnsRecordCreation struct {
	zone          provider.DNSZoneRef
	spec          provider.DNSRecordSpec
	recordID      string
	recoveryToken string
}

func (s Store) SubmitDNSRecordCreation(ctx context.Context, token string, scope identity.Scope, key string, revision int64, input DNSRecordCreationInput) (Operation, error) {
	spec, ok := input.DNSRecordSpec.Normalize()
	if !ok || input.ObservedAt.IsZero() || !provider.ValidDNSRecordID(input.AssetID) {
		return Operation{}, ErrInvalid
	}
	input.DNSRecordSpec = spec
	input.ObservedAt = input.ObservedAt.UTC()
	body, err := json.Marshal(struct {
		DNSRecordCreationInput
		Revision int64
	}{input, revision})
	if err != nil {
		return Operation{}, err
	}
	digest := sha256.Sum256(append([]byte(createDNSRecord+"\x00"), body...))
	return s.submitApprovedAssetChange(ctx, token, scope, key, revision, provider.DNSZone, input.Account, provider.DNSGlobalScope,
		input.AssetID, createDNSRecord, "", digest,
		func(ctx context.Context, tx *sql.Tx, _ identity.Scope, target inventory.MutationTarget) (func(string) error, error) {
			zone, records, err := inventory.DNSCreationTarget(ctx, tx, target, input.AssetID, spec.Name, input.ObservedAt)
			if err != nil {
				return nil, err
			}
			if !publicCloudProvider(target.Provider) || !spec.InZone(zone) {
				return nil, ErrInvalid
			}
			if err := ensureDNSRecordVacant(zone, spec, records); err != nil {
				return nil, err
			}
			encoded, err := json.Marshal(spec)
			if err != nil {
				return nil, err
			}
			return func(id string) error {
				_, err := tx.ExecContext(ctx, `INSERT INTO cmp_dns_record_creation_intents(operation_id,zone_name,spec,recovery_token)
					VALUES ($1,$2,$3::jsonb,$4)`, id, zone.Name, string(encoded), rand.Text())
				return err
			}, nil
		})
}

func ensureDNSRecordVacant(zone provider.DNSZoneRef, spec provider.DNSRecordSpec, records []provider.DNSRecord) error {
	for _, record := range records {
		if spec.ConflictsWith(zone, record) {
			return ErrConflict
		}
	}
	return nil
}

func checkDNSRecordCreation(ctx context.Context, tx *sql.Tx, w *work) (int64, error) {
	change := &w.dnsCreation
	change.zone.ID = w.AssetID
	var scopeID int64
	var observer creationObserver
	var encoded []byte
	err := tx.QueryRowContext(ctx, `SELECT o.scope_id,i.zone_name,i.spec,COALESCE(NULLIF(i.record_id,''),i.reviewed_record_id),i.recovery_token,i.observation_reviewer,
		i.observation_membership_revision,i.observation_credential_revision
		FROM cmp_dns_record_creation_intents AS i JOIN cmp_instance_operations AS o USING(operation_id)
		WHERE i.operation_id=$1`, w.ID).Scan(&scopeID, &change.zone.Name, &encoded, &change.recordID, &change.recoveryToken,
		&observer.principal, &observer.membership, &observer.credential)
	if err != nil {
		return 0, err
	}
	target, beforeWrite, err := resolveApprovedAssetChange(ctx, tx, w, scopeID, observer, provider.DNSZone)
	if err != nil {
		return 0, err
	}
	if err := json.Unmarshal(encoded, &change.spec); err != nil {
		return 0, err
	}
	if normalized, ok := change.spec.Normalize(); !ok || normalized != change.spec ||
		!publicCloudProvider(target.Provider) || !change.spec.InZone(change.zone) {
		return 0, identity.ErrAccountStale
	}
	if err := inventory.CheckDNSZoneName(ctx, tx, scopeID, change.zone); err != nil {
		return 0, err
	}
	if beforeWrite {
		zone, records, err := inventory.DNSCreationTarget(ctx, tx, target, w.AssetID, change.spec.Name, time.Time{})
		if err != nil {
			return 0, err
		}
		if zone != change.zone {
			return 0, identity.ErrAccountStale
		}
		if err := ensureDNSRecordVacant(zone, change.spec, records); err != nil {
			return 0, err
		}
	}
	return scopeID, nil
}

func (s Store) processDNSRecordCreation(ctx context.Context, task mq.Task, w work, client provider.InstanceOperator, scope provider.Scope) (bool, error) {
	scope.Kind = provider.DNSZone
	adapter, ok := client.(provider.DNSRecordCreator)
	if !ok {
		return false, s.endUnavailable(ctx, task, w)
	}
	intent := w.dnsCreation
	if w.Status == "queued" {
		send, err := adapter.PrepareDNSRecordCreation(ctx, scope, intent.zone, intent.spec, intent.recoveryToken)
		if err != nil || send == nil {
			return s.rejectCreationPreflight(ctx, task, w, err)
		}
		return s.dispatchPreparedResource(ctx, task, w, func(ctx context.Context, _ work) (provider.LaunchReceipt, error) {
			return send(ctx)
		})
	}
	if w.Status == "applying" {
		if err := s.transition(ctx, task, "applying", "observing", "submission_outcome_unknown", ""); err != nil {
			return false, err
		}
		w.Status = "observing"
	}
	recordID, err := adapter.ObserveDNSRecordCreation(ctx, scope, intent.zone, intent.spec, intent.recoveryToken, intent.recordID)
	if err == nil && provider.ValidDNSRecordID(recordID) {
		return false, s.confirmDNSRecordCreation(ctx, task, w, recordID)
	}
	if task.Attempts >= task.MaxAttempts || provider.IsFailure(err, provider.InvalidResponse) ||
		provider.IsFailure(err, provider.Unauthorized) || provider.IsFailure(err, provider.Unsupported) ||
		provider.IsFailure(err, provider.PreconditionFailed) {
		return false, s.transition(ctx, task, w.Status, "needs_review", "dns_record_creation_requires_evidence", "")
	}
	return true, nil
}

func (s Store) confirmDNSRecordCreation(ctx context.Context, task mq.Task, w work, recordID string) error {
	err := s.withLease(ctx, task, func(tx *sql.Tx) error {
		scopeID, err := checkDNSRecordCreation(ctx, tx, &w)
		if err != nil {
			return err
		}
		if w.dnsCreation.recordID != "" && w.dnsCreation.recordID != recordID {
			return writeTransition(ctx, tx, w.ID, w.Status, "needs_review", "creation_receipt_mismatch", "")
		}
		if _, err := tx.ExecContext(ctx, `UPDATE cmp_dns_record_creation_intents SET record_id=$2 WHERE operation_id=$1`, w.ID, recordID); err != nil {
			return err
		}
		if err := inventory.InvalidateDNSRecords(ctx, tx, scopeID, w.AssetID); err != nil {
			return err
		}
		return writeTransition(ctx, tx, w.ID, w.Status, "succeeded", "dns_record_creation_observed", "")
	})
	if errors.Is(err, identity.ErrForbidden) || errors.Is(err, identity.ErrAccountStale) || errors.Is(err, ErrConflict) {
		return s.endUnavailable(ctx, task, w)
	}
	return err
}
