package operations

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/birdordev/plumego/x/messaging/mq"

	"with-cloud-manager/internal/dbschema"
	"with-cloud-manager/internal/identity"
	"with-cloud-manager/internal/inventory"
	"with-cloud-manager/internal/provider"
)

const DNSRecordUpdateMigration = "0165_dns_record_update"
const updateDNSRecord = "update-dns-record"

func MigrateDNSRecordUpdate(ctx context.Context, db *sql.DB) error {
	return dbschema.Apply(ctx, db, DNSRecordUpdateMigration)
}

type DNSRecordUpdateInput struct {
	Account    string    `json:"account"`
	AssetID    string    `json:"asset_id"`
	RecordID   string    `json:"record_id"`
	ObservedAt time.Time `json:"observed_at"`
	Values     []string  `json:"values"`
	TTL        int       `json:"ttl"`
}

type DNSRecordUpdateView struct {
	ZoneName string             `json:"zone_name"`
	Record   provider.DNSRecord `json:"record"`
	Values   []string           `json:"target_values"`
	TTL      int                `json:"target_ttl"`
}

func (s Store) SubmitDNSRecordUpdate(ctx context.Context, token string, scope identity.Scope, key string, revision int64, input DNSRecordUpdateInput) (Operation, error) {
	values, valid := provider.NormalizeDNSUpdateValues(input.Values)
	if !valid || input.TTL < 1 || input.TTL > 604800 || input.ObservedAt.IsZero() ||
		!provider.ValidDNSRecordID(input.AssetID) || !provider.ValidDNSRecordID(input.RecordID) {
		return Operation{}, ErrInvalid
	}
	input.ObservedAt = input.ObservedAt.UTC()
	input.Values = values
	body, err := json.Marshal(struct {
		DNSRecordUpdateInput
		Revision int64
	}{input, revision})
	if err != nil {
		return Operation{}, err
	}
	digest := sha256.Sum256(append([]byte(updateDNSRecord+"\x00"), body...))
	return s.submitApprovedAssetChange(ctx, token, scope, key, revision, provider.DNSZone, input.Account, provider.DNSGlobalScope,
		input.AssetID, updateDNSRecord, "", digest,
		func(ctx context.Context, tx *sql.Tx, _ identity.Scope, target inventory.MutationTarget) (func(string) error, error) {
			zone, record, err := inventory.DNSRecordTarget(ctx, tx, target, input.AssetID, input.RecordID, input.ObservedAt)
			if err != nil {
				return nil, err
			}
			change := provider.DNSRecordUpdate{Zone: zone, Record: record, Values: values, TTL: input.TTL}
			if !change.ValidFor(target.Provider) {
				return nil, ErrInvalid
			}
			original, err := json.Marshal(record)
			if err != nil {
				return nil, err
			}
			desired, err := json.Marshal(values)
			if err != nil {
				return nil, err
			}
			return func(id string) error {
				_, err := tx.ExecContext(ctx, `INSERT INTO cmp_dns_record_update_intents(operation_id,zone_name,record,target_values,target_ttl)
					VALUES ($1,$2,$3::jsonb,$4::jsonb,$5)`, id, zone.Name, string(original), string(desired), input.TTL)
				return err
			}, nil
		})
}

func checkDNSRecordUpdate(ctx context.Context, tx *sql.Tx, w *work) (int64, error) {
	change := &w.dnsUpdate
	change.Zone.ID = w.AssetID
	var scopeID int64
	var observer creationObserver
	var original, desired []byte
	err := tx.QueryRowContext(ctx, `SELECT o.scope_id,i.zone_name,i.record,i.target_values,i.target_ttl,
		i.observation_reviewer,i.observation_membership_revision,i.observation_credential_revision
		FROM cmp_dns_record_update_intents AS i JOIN cmp_instance_operations AS o USING(operation_id)
		WHERE i.operation_id=$1`, w.ID).Scan(&scopeID, &change.Zone.Name, &original, &desired, &change.TTL,
		&observer.principal, &observer.membership, &observer.credential)
	if err != nil {
		return 0, err
	}
	target, beforeWrite, err := resolveApprovedAssetChange(ctx, tx, w, scopeID, observer, provider.DNSZone)
	if err != nil {
		return 0, err
	}
	if err := json.Unmarshal(original, &change.Record); err != nil {
		return 0, err
	}
	if err := json.Unmarshal(desired, &change.Values); err != nil {
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

func (s Store) processDNSRecordUpdate(ctx context.Context, task mq.Task, w work, client provider.InstanceOperator, scope provider.Scope) (bool, error) {
	scope.Kind = provider.DNSZone
	adapter, ok := client.(provider.DNSRecordUpdater)
	if !ok {
		return false, s.endUnavailable(ctx, task, w)
	}
	if w.Status == "queued" {
		apply, err := adapter.PrepareDNSRecordUpdate(ctx, scope, w.dnsUpdate)
		return s.dispatchSingleMutation(ctx, task, w, apply, err, "dns_record_update")
	}
	if w.Status == "applying" {
		if err := s.transition(ctx, task, "applying", "observing", "submission_outcome_unknown", ""); err != nil {
			return false, err
		}
		w.Status = "observing"
	}
	status, err := adapter.ObserveDNSRecordUpdate(ctx, scope, w.dnsUpdate)
	if err == nil && status == provider.ActionSucceeded {
		return false, s.confirmDNSRecordUpdate(ctx, task, w)
	}
	if task.Attempts >= task.MaxAttempts || provider.IsFailure(err, provider.InvalidResponse) ||
		provider.IsFailure(err, provider.Unauthorized) || provider.IsFailure(err, provider.Unsupported) ||
		provider.IsFailure(err, provider.PreconditionFailed) || provider.IsFailure(err, provider.Absent) {
		return false, s.transition(ctx, task, w.Status, "needs_review", "dns_record_update_requires_evidence", "")
	}
	return true, nil
}

func (s Store) confirmDNSRecordUpdate(ctx context.Context, task mq.Task, w work) error {
	err := s.withLease(ctx, task, func(tx *sql.Tx) error {
		scopeID, err := checkDNSRecordUpdate(ctx, tx, &w)
		if err != nil {
			return err
		}
		if err := inventory.InvalidateDNSRecords(ctx, tx, scopeID, w.AssetID); err != nil {
			return err
		}
		return writeTransition(ctx, tx, w.ID, w.Status, "succeeded", "dns_record_update_observed", "")
	})
	if errors.Is(err, identity.ErrForbidden) || errors.Is(err, identity.ErrAccountStale) || errors.Is(err, ErrConflict) {
		return s.endUnavailable(ctx, task, w)
	}
	return err
}
