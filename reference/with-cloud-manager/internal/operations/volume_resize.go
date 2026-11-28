package operations

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"

	"github.com/birdordev/plumego/x/messaging/mq"

	"with-cloud-manager/internal/dbschema"
	"with-cloud-manager/internal/identity"
	"with-cloud-manager/internal/inventory"
	"with-cloud-manager/internal/provider"
)

const VolumeResizeMigration = "0157_volume_resize"

func MigrateVolumeResize(ctx context.Context, db *sql.DB) error {
	return dbschema.Apply(ctx, db, VolumeResizeMigration)
}

const resizeVolume = "resize-volume"

type VolumeResizeInput struct {
	Account    string `json:"account"`
	Region     string `json:"region"`
	AssetID    string `json:"asset_id"`
	CurrentGiB int    `json:"current_gib"`
	TargetGiB  int    `json:"target_gib"`
}

type VolumeResizeView struct {
	CurrentGiB        int   `json:"current_gib"`
	TargetGiB         int   `json:"target_gib"`
	OwnershipRevision int64 `json:"ownership_revision"`
}

func (s Store) SubmitVolumeResize(ctx context.Context, token string, scope identity.Scope, key string, revision int64, input VolumeResizeInput) (Operation, error) {
	change := provider.VolumeResize{ID: input.AssetID, CurrentGiB: input.CurrentGiB, TargetGiB: input.TargetGiB}
	if !change.Valid() {
		return Operation{}, ErrInvalid
	}
	data, err := json.Marshal(struct {
		VolumeResizeInput
		Revision int64
	}{input, revision})
	if err != nil {
		return Operation{}, err
	}
	digest := sha256.Sum256(append([]byte(resizeVolume+"\x00"), data...))
	return s.submitApprovedAssetChange(ctx, token, scope, key, revision, provider.Volume,
		input.Account, input.Region, input.AssetID, resizeVolume, "", digest,
		func(ctx context.Context, tx *sql.Tx, _ identity.Scope, target inventory.MutationTarget) (func(string) error, error) {
			if !publicCloudProvider(target.Provider) {
				return nil, ErrInvalid
			}
			if err := checkVolumeResizeTarget(ctx, tx, target, change, true); err != nil {
				return nil, err
			}
			return func(id string) error {
				_, err := tx.ExecContext(ctx, `INSERT INTO cmp_volume_resize_intents(operation_id,current_gib,target_gib)
					VALUES ($1,$2,$3)`, id, change.CurrentGiB, change.TargetGiB)
				return err
			}, nil
		})
}

func checkVolumeResizeTarget(ctx context.Context, tx *sql.Tx, target inventory.MutationTarget, change provider.VolumeResize, beforeWrite bool) error {
	size, detached, err := inventory.VolumeResizeTarget(ctx, tx, target, change.ID)
	if err != nil {
		return err
	}
	if size != change.CurrentGiB && (beforeWrite || size != change.TargetGiB) {
		return identity.ErrAccountStale
	}
	if beforeWrite && !detached {
		return ErrConflict
	}
	var busy bool
	err = tx.QueryRowContext(ctx, `SELECT EXISTS (
		SELECT 1 FROM cmp_instance_operations WHERE (volume_scope_id,volume_id)=($1,$2) AND status IN `+
		activeOperationStatuses+`) OR `+snapshotWriteConflict+" OR "+retirementDiskReserved, target.ScopeID, change.ID, "").Scan(&busy)
	if err == nil && busy {
		return ErrConflict
	}
	return err
}

func checkVolumeResize(ctx context.Context, tx *sql.Tx, w *work) error {
	var scopeID int64
	var observer creationObserver
	change := &w.volumeResize
	change.ID = w.AssetID
	err := tx.QueryRowContext(ctx, `SELECT o.scope_id,i.current_gib,i.target_gib,
		i.observation_reviewer,i.observation_membership_revision,i.observation_credential_revision
		FROM cmp_volume_resize_intents AS i JOIN cmp_instance_operations AS o USING(operation_id)
		WHERE operation_id=$1`, w.ID).Scan(&scopeID, &change.CurrentGiB, &change.TargetGiB,
		&observer.principal, &observer.membership, &observer.credential)
	if err != nil {
		return err
	}
	target, beforeWrite, err := resolveApprovedAssetChange(ctx, tx, w, scopeID, observer, provider.Volume)
	if err != nil {
		return err
	}
	if !change.Valid() || !publicCloudProvider(target.Provider) {
		return ErrConflict
	}
	return checkVolumeResizeTarget(ctx, tx, target, *change, beforeWrite)
}

func (s Store) processVolumeResize(ctx context.Context, task mq.Task, w work, client provider.InstanceOperator, scope provider.Scope) (bool, error) {
	scope.Kind = provider.Volume
	resizer, ok := client.(provider.VolumeResizer)
	if !ok {
		return false, s.endUnavailable(ctx, task, w)
	}
	if w.Status == "queued" {
		apply, err := resizer.PrepareVolumeResize(ctx, scope, w.volumeResize)
		return s.dispatchSingleMutation(ctx, task, w, apply, err, "volume_resize")
	}
	if w.Status == "applying" {
		if err := s.transition(ctx, task, "applying", "observing", "submission_outcome_unknown", ""); err != nil {
			return false, err
		}
		w.Status = "observing"
	}
	status, err := resizer.ObserveVolumeResize(ctx, scope, w.volumeResize, w.ProviderRequestID)
	if err == nil && status == provider.ActionSucceeded {
		return false, s.confirmVolumeResize(ctx, task, w)
	}
	if status == provider.ActionFailed || task.Attempts >= task.MaxAttempts || provider.IsFailure(err, provider.InvalidResponse) ||
		provider.IsFailure(err, provider.Unauthorized) || provider.IsFailure(err, provider.Unsupported) {
		return false, s.transition(ctx, task, w.Status, "needs_review", "volume_resize_requires_evidence", "")
	}
	return true, nil
}

func (s Store) confirmVolumeResize(ctx context.Context, task mq.Task, w work) error {
	err := s.withLease(ctx, task, func(tx *sql.Tx) error {
		scopeID, err := inventory.LockVolumeScope(ctx, tx, w.actor.Tenant, w.Account, w.Region)
		if err != nil {
			return err
		}
		if err := checkVolumeResize(ctx, tx, &w); err != nil {
			return err
		}
		if err := inventory.RecordObservedVolumeSize(ctx, tx, scopeID, w.volumeResize); err != nil {
			return err
		}
		return writeTransition(ctx, tx, w.ID, w.Status, "succeeded", "volume_capacity_observed", "")
	})
	if errors.Is(err, identity.ErrForbidden) || errors.Is(err, identity.ErrAccountStale) || errors.Is(err, ErrConflict) {
		return s.endUnavailable(ctx, task, w)
	}
	return err
}
