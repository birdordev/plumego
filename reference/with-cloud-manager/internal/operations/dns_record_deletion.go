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

const deleteDNSRecord = "delete-dns-record"

func isDNSRecordMutation(action string) bool {
	return action == createDNSRecord || action == setDNSRecordStatus || action == deleteDNSRecord || action == updateDNSRecord
}

type DNSRecordDeletionInput struct {
	Account    string    `json:"account"`
	AssetID    string    `json:"asset_id"`
	RecordID   string    `json:"record_id"`
	ObservedAt time.Time `json:"observed_at"`
}

type DNSRecordDeletionView struct {
	ZoneName string             `json:"zone_name"`
	Record   provider.DNSRecord `json:"record"`
}

func (s Store) SubmitDNSRecordDeletion(ctx context.Context, token string, scope identity.Scope, key string, revision int64, input DNSRecordDeletionInput) (Operation, error) {
	if input.ObservedAt.IsZero() || !provider.ValidDNSRecordID(input.RecordID) || !provider.ValidDNSRecordID(input.AssetID) {
		return Operation{}, ErrInvalid
	}
	input.ObservedAt = input.ObservedAt.UTC()
	body, err := json.Marshal(struct {
		DNSRecordDeletionInput
		Revision int64
	}{input, revision})
	if err != nil {
		return Operation{}, err
	}
	digest := sha256.Sum256(append([]byte(deleteDNSRecord+"\x00"), body...))
	return s.submitApprovedAssetChange(ctx, token, scope, key, revision, provider.DNSZone, input.Account, provider.DNSGlobalScope,
		input.AssetID, deleteDNSRecord, "", digest,
		func(ctx context.Context, tx *sql.Tx, _ identity.Scope, target inventory.MutationTarget) (func(string) error, error) {
			zone, record, err := inventory.DNSRecordTarget(ctx, tx, target, input.AssetID, input.RecordID, input.ObservedAt)
			if err != nil {
				return nil, err
			}
			targetRecord := provider.DNSRecordDeletion{Zone: zone, Record: record}
			if !targetRecord.Valid() {
				return nil, ErrInvalid
			}
			encoded, err := json.Marshal(record)
			if err != nil {
				return nil, err
			}
			return func(id string) error {
				_, err := tx.ExecContext(ctx, `INSERT INTO cmp_dns_record_deletion_intents(operation_id,zone_name,record)
					VALUES ($1,$2,$3::jsonb)`, id, zone.Name, string(encoded))
				return err
			}, nil
		})
}

func checkDNSRecordDeletion(ctx context.Context, tx *sql.Tx, w *work) (int64, error) {
	deletion := &w.dnsDeletion
	deletion.Zone.ID = w.AssetID
	var scopeID int64
	var observer creationObserver
	var encoded []byte
	err := tx.QueryRowContext(ctx, `SELECT o.scope_id,i.zone_name,i.record,i.observation_reviewer,
		i.observation_membership_revision,i.observation_credential_revision
		FROM cmp_dns_record_deletion_intents AS i JOIN cmp_instance_operations AS o USING(operation_id)
		WHERE i.operation_id=$1`, w.ID).Scan(&scopeID, &deletion.Zone.Name, &encoded,
		&observer.principal, &observer.membership, &observer.credential)
	if err != nil {
		return 0, err
	}
	target, beforeWrite, err := resolveApprovedAssetChange(ctx, tx, w, scopeID, observer, provider.DNSZone)
	if err != nil {
		return 0, err
	}
	if err := json.Unmarshal(encoded, &deletion.Record); err != nil {
		return 0, err
	}
	if !deletion.Valid() {
		return 0, identity.ErrAccountStale
	}
	if err := inventory.CheckDNSZoneName(ctx, tx, scopeID, deletion.Zone); err != nil {
		return 0, err
	}
	if beforeWrite {
		zone, record, err := inventory.DNSRecordTarget(ctx, tx, target, w.AssetID, deletion.Record.ID, time.Time{})
		if err != nil {
			return 0, err
		}
		if zone != deletion.Zone || record.Status != deletion.Record.Status || !deletion.Record.MatchesAddressRecord(record) {
			return 0, identity.ErrAccountStale
		}
	}
	return scopeID, nil
}

func (s Store) processDNSRecordDeletion(ctx context.Context, task mq.Task, w work, client provider.InstanceOperator, scope provider.Scope) (bool, error) {
	scope.Kind = provider.DNSZone
	adapter, ok := client.(provider.DNSRecordDeleter)
	if !ok {
		return false, s.endUnavailable(ctx, task, w)
	}
	if w.Status == "queued" {
		apply, err := adapter.PrepareDNSRecordDeletion(ctx, scope, w.dnsDeletion)
		return s.dispatchSingleMutation(ctx, task, w, apply, err, "dns_record_deletion")
	}
	if w.Status == "applying" {
		if err := s.transition(ctx, task, "applying", "observing", "submission_outcome_unknown", ""); err != nil {
			return false, err
		}
		w.Status = "observing"
	}
	status, observeErr := adapter.ObserveDNSRecordDeletion(ctx, scope, w.dnsDeletion)
	return s.recordDNSDeletionObservation(ctx, task, w, status, observeErr)
}

func (s Store) recordDNSDeletionObservation(ctx context.Context, task mq.Task, w work, status provider.ActionResult, observeErr error) (bool, error) {
	var terminal bool
	accounting, cancel := context.WithTimeout(context.WithoutCancel(ctx), accountingTimeout)
	defer cancel()
	err := s.withLease(accounting, task, func(tx *sql.Tx) error {
		scopeID, err := checkDNSRecordDeletion(accounting, tx, &w)
		if err != nil {
			return err
		}
		var observations int16
		// 租约回收会增加尝试编号，避免把跨过未完成读取的两次观测计为连续。
		if err := tx.QueryRowContext(accounting, `UPDATE cmp_dns_record_deletion_intents SET
			absence_observations=CASE WHEN NOT $2::bool THEN 0 WHEN last_absence_attempt=$3::integer-1
				THEN LEAST(absence_observations+1,2) ELSE 1 END,
			last_absence_attempt=CASE WHEN $2::bool THEN $3::integer ELSE 0 END
			WHERE operation_id=$1 RETURNING absence_observations`, w.ID,
			observeErr == nil && status == provider.ActionSucceeded, task.Attempts).Scan(&observations); err != nil {
			return err
		}
		if observations == 2 {
			terminal = true
			if err := inventory.InvalidateDNSRecords(accounting, tx, scopeID, w.AssetID); err != nil {
				return err
			}
			return writeTransition(accounting, tx, w.ID, w.Status, "succeeded", "dns_record_absence_confirmed", "")
		}
		if status == provider.ActionFailed || task.Attempts >= task.MaxAttempts ||
			provider.IsFailure(observeErr, provider.InvalidResponse) || provider.IsFailure(observeErr, provider.Unauthorized) ||
			provider.IsFailure(observeErr, provider.Unsupported) || provider.IsFailure(observeErr, provider.PreconditionFailed) {
			terminal = true
			return writeTransition(accounting, tx, w.ID, w.Status, "needs_review", "dns_record_deletion_requires_evidence", "")
		}
		return nil
	})
	if errors.Is(err, identity.ErrForbidden) || errors.Is(err, identity.ErrAccountStale) || errors.Is(err, ErrConflict) {
		return false, s.endUnavailable(accounting, task, w)
	}
	return err == nil && !terminal, err
}
