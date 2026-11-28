package operations

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/birdordev/plumego/x/messaging/mq"

	"with-cloud-manager/internal/identity"
	"with-cloud-manager/internal/inventory"
	"with-cloud-manager/internal/provider"
)

const setDNSRecordStatus = "set-dns-record-status"

type DNSRecordStatusInput struct {
	Account    string    `json:"account"`
	AssetID    string    `json:"asset_id"`
	RecordID   string    `json:"record_id"`
	ObservedAt time.Time `json:"observed_at"`
	Enabled    *bool     `json:"enabled"`
}

type DNSRecordStatusView struct {
	ZoneName string `json:"zone_name"`
	RecordID string `json:"record_id"`
	Name     string `json:"name"`
	Current  string `json:"current_status"`
	Enabled  bool   `json:"enabled"`
}

func (s Store) SubmitDNSRecordStatus(ctx context.Context, token string, scope identity.Scope, key string, revision int64, input DNSRecordStatusInput) (Operation, error) {
	if input.Enabled == nil || input.ObservedAt.IsZero() || !provider.ValidDNSRecordID(input.RecordID) ||
		!provider.ValidDNSRecordID(input.AssetID) {
		return Operation{}, ErrInvalid
	}
	input.ObservedAt = input.ObservedAt.UTC()
	body, err := json.Marshal(struct {
		DNSRecordStatusInput
		Revision int64
	}{input, revision})
	if err != nil {
		return Operation{}, err
	}
	digest := sha256.Sum256(append([]byte(setDNSRecordStatus+"\x00"), body...))
	return s.submitApprovedAssetChange(ctx, token, scope, key, revision, provider.DNSZone, input.Account, provider.DNSGlobalScope,
		input.AssetID, setDNSRecordStatus, "", digest,
		func(ctx context.Context, tx *sql.Tx, _ identity.Scope, target inventory.MutationTarget) (func(string) error, error) {
			zone, record, err := inventory.DNSRecordTarget(ctx, tx, target, input.AssetID, input.RecordID, input.ObservedAt)
			if err != nil {
				return nil, err
			}
			change := provider.DNSRecordStatusChange{Zone: zone, Record: record, Enabled: *input.Enabled}
			if !change.ValidFor(target.Provider) {
				return nil, ErrInvalid
			}
			encoded, err := json.Marshal(record)
			if err != nil {
				return nil, err
			}
			return func(id string) error {
				_, err := tx.ExecContext(ctx, `INSERT INTO cmp_dns_record_status_intents(operation_id,zone_name,record,enabled)
					VALUES ($1,$2,$3::jsonb,$4)`, id, zone.Name, string(encoded), *input.Enabled)
				return err
			}, nil
		})
}

func checkDNSRecordStatus(ctx context.Context, tx *sql.Tx, w *work) (int64, error) {
	change := &w.dnsStatus
	change.Zone.ID = w.AssetID
	var scopeID int64
	var observer creationObserver
	var encoded []byte
	err := tx.QueryRowContext(ctx, `SELECT o.scope_id,i.zone_name,i.record,i.enabled,i.observation_reviewer,
		i.observation_membership_revision,i.observation_credential_revision
		FROM cmp_dns_record_status_intents AS i JOIN cmp_instance_operations AS o USING(operation_id)
		WHERE i.operation_id=$1`, w.ID).Scan(&scopeID, &change.Zone.Name, &encoded, &change.Enabled,
		&observer.principal, &observer.membership, &observer.credential)
	if err != nil {
		return 0, err
	}
	target, beforeWrite, err := resolveApprovedAssetChange(ctx, tx, w, scopeID, observer, provider.DNSZone)
	if err != nil {
		return 0, err
	}
	if err := json.Unmarshal(encoded, &change.Record); err != nil {
		return 0, err
	}
	if !change.ValidFor(target.Provider) {
		return 0, identity.ErrAccountStale
	}
	if err := inventory.CheckDNSZoneName(ctx, tx, scopeID, change.Zone); err != nil {
		return 0, err
	}
	if beforeWrite {
		zone, record, err := inventory.DNSRecordTarget(ctx, tx, target, w.AssetID, change.Record.ID, time.Time{})
		if err != nil {
			return 0, err
		}
		if zone != change.Zone || record.Status != change.Record.Status || !change.Record.MatchesAddressRecord(record) {
			return 0, identity.ErrAccountStale
		}
	}
	return scopeID, nil
}

func (s Store) processDNSRecordStatus(ctx context.Context, task mq.Task, w work, client provider.InstanceOperator, scope provider.Scope) (bool, error) {
	scope.Kind = provider.DNSZone
	adapter, ok := client.(provider.DNSRecordStatusOperator)
	if !ok {
		return false, s.endUnavailable(ctx, task, w)
	}
	if w.Status == "queued" {
		apply, err := adapter.PrepareDNSRecordStatus(ctx, scope, w.dnsStatus)
		return s.dispatchSingleMutation(ctx, task, w, apply, err, "dns_record_status")
	}
	if w.Status == "applying" {
		if err := s.transition(ctx, task, "applying", "observing", "submission_outcome_unknown", ""); err != nil {
			return false, err
		}
		w.Status = "observing"
	}
	status, err := adapter.ObserveDNSRecordStatus(ctx, scope, w.dnsStatus)
	if err == nil && status == provider.ActionSucceeded {
		return false, s.confirmDNSRecordStatus(ctx, task, w)
	}
	if status == provider.ActionFailed || task.Attempts >= task.MaxAttempts ||
		provider.IsFailure(err, provider.InvalidResponse) || provider.IsFailure(err, provider.Unauthorized) ||
		provider.IsFailure(err, provider.Unsupported) || provider.IsFailure(err, provider.PreconditionFailed) {
		return false, s.transition(ctx, task, w.Status, "needs_review", "dns_record_status_requires_evidence", "")
	}
	return true, nil
}

func (s Store) confirmDNSRecordStatus(ctx context.Context, task mq.Task, w work) error {
	err := s.withLease(ctx, task, func(tx *sql.Tx) error {
		scopeID, err := checkDNSRecordStatus(ctx, tx, &w)
		if err != nil {
			return err
		}
		if err := inventory.InvalidateDNSRecords(ctx, tx, scopeID, w.AssetID); err != nil {
			return err
		}
		return writeTransition(ctx, tx, w.ID, w.Status, "succeeded", "dns_record_status_observed", "")
	})
	if errors.Is(err, identity.ErrForbidden) || errors.Is(err, identity.ErrAccountStale) || errors.Is(err, ErrConflict) {
		return s.endUnavailable(ctx, task, w)
	}
	return err
}
