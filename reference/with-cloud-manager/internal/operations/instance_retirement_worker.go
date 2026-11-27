package operations

import (
	"context"
	"database/sql"
	"errors"

	"github.com/birdordev/plumego/x/messaging/mq"

	"with-cloud-manager/internal/identity"
	"with-cloud-manager/internal/inventory"
	"with-cloud-manager/internal/provider"
)

func checkInstanceRetirement(ctx context.Context, tx *sql.Tx, w *work) error {
	var scopeID, diskRevision int64
	var observer creationObserver
	w.retirement.InstanceID = w.AssetID
	err := tx.QueryRowContext(ctx, `SELECT o.scope_id,i.system_disk_id,i.disk_ownership_revision,
		i.observation_reviewer,i.observation_membership_revision,i.observation_credential_revision
		FROM cmp_instance_retirement_intents AS i JOIN cmp_instance_operations AS o USING(operation_id)
		WHERE operation_id=$1`, w.ID).Scan(&scopeID, &w.retirement.SystemDiskID, &diskRevision,
		&observer.principal, &observer.membership, &observer.credential)
	if err != nil {
		return err
	}
	target, beforeWrite, err := resolveApprovedAssetChange(ctx, tx, w, scopeID, observer, provider.Instance)
	if err != nil {
		return err
	}
	if !w.retirement.ValidFor(target.Provider) {
		return identity.ErrAccountStale
	}
	if !beforeWrite {
		return nil
	}
	id, err := retirementDiskID(ctx, tx, target, w.AssetID)
	if err != nil {
		return err
	}
	if id != w.retirement.SystemDiskID {
		return identity.ErrAccountStale
	}
	disk, err := resolveRetirementDisk(ctx, tx, w.actor, w.Account, w.Region, id)
	if err == nil && disk.OwnershipRevision != diskRevision {
		return identity.ErrAccountStale
	}
	return err
}

func (s Store) processInstanceRetirement(ctx context.Context, task mq.Task, w work, client provider.InstanceOperator, scope provider.Scope) (bool, error) {
	if w.Status == "applying" {
		if err := s.transition(ctx, task, "applying", "observing", "submission_outcome_unknown", ""); err != nil {
			return false, err
		}
		w.Status = "observing"
	}
	retirer, ok := client.(provider.InstanceRetirer)
	if !ok {
		return false, s.endUnavailable(ctx, task, w)
	}
	if w.Status == "queued" {
		send, err := retirer.PrepareInstanceRetirement(ctx, scope, w.retirement)
		return s.dispatchSingleMutation(ctx, task, w, send, err, "instance_retirement")
	}
	if !requestIDPattern.MatchString(w.ProviderRequestID) {
		return false, s.transition(ctx, task, w.Status, "needs_review", "instance_retirement_receipt_required", "")
	}
	result, err := retirer.ObserveInstanceRetirement(ctx, scope, w.retirement, w.ProviderRequestID)
	review := result == provider.ActionFailed || task.Attempts >= task.MaxAttempts ||
		provider.IsFailure(err, provider.Unauthorized) || provider.IsFailure(err, provider.Unsupported) || provider.IsFailure(err, provider.InvalidResponse)
	return s.recordInstanceRetirementObservation(ctx, task, w, err == nil && result == provider.ActionSucceeded, review)
}

func (s Store) recordInstanceRetirementObservation(ctx context.Context, task mq.Task, w work, absent, review bool) (bool, error) {
	finished := false
	err := s.withLease(ctx, task, func(tx *sql.Tx) error {
		scopeID, err := inventory.LockInstanceScope(ctx, tx, w.actor.Tenant, w.Account, w.Region)
		if err != nil {
			return err
		}
		if err := checkInstanceRetirement(ctx, tx, &w); err != nil {
			return err
		}
		var count int
		if err := tx.QueryRowContext(ctx, `UPDATE cmp_instance_retirement_intents SET
			absence_observations=CASE WHEN NOT $2::bool THEN 0
				WHEN last_absence_attempt=$3 THEN absence_observations
				WHEN last_absence_attempt=$3-1 THEN LEAST(absence_observations+1,2)
				ELSE 1 END,
			last_absence_attempt=CASE WHEN $2::bool THEN $3 ELSE 0 END
			WHERE operation_id=$1 RETURNING absence_observations`, w.ID, absent, task.Attempts).Scan(&count); err != nil {
			return err
		}
		if count == 2 {
			finished = true
			if err := recordConfirmedInstanceDeletion(ctx, tx, scopeID, w.AssetID); err != nil {
				return err
			}
			return writeTransition(ctx, tx, w.ID, w.Status, "succeeded", "instance_retirement_observed", "")
		}
		if review {
			finished = true
			return writeTransition(ctx, tx, w.ID, w.Status, "needs_review", "instance_retirement_requires_evidence", "")
		}
		return nil
	})
	if errors.Is(err, identity.ErrForbidden) || errors.Is(err, identity.ErrAccountStale) || errors.Is(err, ErrConflict) {
		return false, s.endUnavailable(ctx, task, w)
	}
	return !finished, err
}
