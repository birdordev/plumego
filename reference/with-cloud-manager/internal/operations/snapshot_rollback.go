package operations

import (
	"context"
	"database/sql"
	_ "embed"
	"errors"

	"github.com/birdordev/plumego/x/messaging/mq"

	"with-cloud-manager/internal/identity"
	"with-cloud-manager/internal/inventory"
	"with-cloud-manager/internal/provider"
)

//go:embed 0045_snapshot_rollback.sql
var snapshotRollbackSchema string

const rollbackSnapshot = "rollback-snapshot"

type SnapshotRollback struct {
	SnapshotDeletion
	ConfirmOverwrite bool `json:"confirm_overwrite"`
}

func (s Store) SubmitSnapshotRollback(ctx context.Context, token string, scope identity.Scope, key string, revision int64, input SnapshotRollback) (Operation, error) {
	if !input.ConfirmOverwrite {
		return Operation{}, ErrInvalid
	}
	return s.submitSnapshotMutation(ctx, token, scope, key, revision, input.SnapshotDeletion, rollbackSnapshot)
}

func resolveSnapshotWrite(ctx context.Context, tx *sql.Tx, actor identity.Scope, account, region, snapshotID, volumeID, action string) (inventory.SnapshotMutationTarget, error) {
	if action == rollbackSnapshot {
		volume, err := inventory.ResolveVolumeMutation(ctx, tx, actor, account, region, volumeID)
		if err != nil {
			return inventory.SnapshotMutationTarget{}, err
		}
		var busy bool
		err = tx.QueryRowContext(ctx, `SELECT EXISTS (
			SELECT 1 FROM cmp_instance_operations WHERE (volume_scope_id,volume_id)=($1,$2)
			AND status IN `+activeOperationStatuses+`) OR `+retirementDiskReserved, volume.ScopeID, volumeID).Scan(&busy)
		if err != nil {
			return inventory.SnapshotMutationTarget{}, err
		}
		if busy {
			return inventory.SnapshotMutationTarget{}, ErrConflict
		}
	}
	return inventory.ResolveSnapshotMutation(ctx, tx, actor, account, region, snapshotID, volumeID, identity.Execute, true)
}

func (s Store) processSnapshotRollback(ctx context.Context, task mq.Task, w work, client provider.InstanceOperator, scope provider.Scope) (bool, error) {
	operator, ok := client.(provider.SnapshotRollbackOperator)
	if !ok {
		return false, s.endUnavailable(ctx, task, w)
	}
	scope.Kind = provider.Snapshot
	if w.Status == "queued" {
		send, err := operator.PrepareSnapshotRollback(ctx, scope, w.snapshot)
		return s.dispatchSingleMutation(ctx, task, w, send, err, "snapshot_rollback")
	}
	if w.Status == "applying" {
		if err := s.transition(ctx, task, "applying", "observing", "submission_outcome_unknown", ""); err != nil {
			return false, err
		}
		w.Status = "observing"
	}
	if scope.Provider != provider.HuaweiECS || w.snapshot.Class != "standard" || w.ProviderRequestID == "" {
		return false, s.transition(ctx, task, w.Status, "needs_review", "snapshot_rollback_requires_evidence", "")
	}
	result, err := operator.ObserveSnapshotRollback(ctx, scope, w.snapshot, w.ProviderRequestID)
	if err == nil {
		switch result {
		case provider.ActionSucceeded:
			return false, s.finishSnapshotRollback(ctx, task, w, "succeeded", "snapshot_rollback_job_succeeded")
		case provider.ActionFailed:
			return false, s.finishSnapshotRollback(ctx, task, w, "failed", "snapshot_rollback_job_failed")
		}
	}
	if task.Attempts >= task.MaxAttempts || provider.IsFailure(err, provider.InvalidResponse) || provider.IsFailure(err, provider.Unauthorized) {
		return false, s.transition(ctx, task, w.Status, "needs_review", "snapshot_rollback_requires_evidence", "")
	}
	return true, nil
}

func (s Store) finishSnapshotRollback(ctx context.Context, task mq.Task, w work, status, reason string) error {
	err := s.withLease(ctx, task, func(tx *sql.Tx) error {
		if err := checkSnapshotMutation(ctx, tx, &w); err != nil {
			return err
		}
		return writeTransition(ctx, tx, w.ID, w.Status, status, reason, "")
	})
	if errors.Is(err, identity.ErrForbidden) || errors.Is(err, identity.ErrAccountStale) {
		return s.endUnavailable(ctx, task, w)
	}
	return err
}
