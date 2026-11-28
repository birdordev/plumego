package operations

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"regexp"

	"github.com/birdordev/plumego/x/messaging/mq"

	"with-cloud-manager/internal/dbschema"
	"with-cloud-manager/internal/identity"
	"with-cloud-manager/internal/inventory"
	"with-cloud-manager/internal/provider"
)

var attachDevice = regexp.MustCompile(`^/dev/sd[f-p]$`)
var ErrVolumeStale = fmt.Errorf("volume ownership revision changed: %w", identity.ErrAccountStale)

func migrateVolume(ctx context.Context, db *sql.DB) error {
	return dbschema.Apply(ctx, db, "0018_volume_operations")
}

func volumeAction(action string) bool {
	return action == "attach-volume" || action == "detach-volume"
}

func validAction(input Input) bool {
	switch input.Action {
	case "attach-volume":
		return operationKey.MatchString(input.VolumeID) && input.VolumeRevision > 0 && !input.GuestUnmounted &&
			(input.Device == "" || attachDevice.MatchString(input.Device))
	case "detach-volume":
		return operationKey.MatchString(input.VolumeID) && input.VolumeRevision > 0 && input.GuestUnmounted && input.Device == ""
	case "start", "stop", "reboot", "delete":
		return input.VolumeID == "" && input.VolumeRevision == 0 && input.Device == "" && !input.GuestUnmounted
	}
	return false
}

func resolveOperationVolume(ctx context.Context, tx *sql.Tx, actor identity.Scope, input Input, vendor string) (inventory.MutationTarget, error) {
	return inspectOperationVolume(ctx, tx, actor, input, vendor, true)
}

func inspectOperationVolume(ctx context.Context, tx *sql.Tx, actor identity.Scope, input Input, vendor string, lock bool) (inventory.MutationTarget, error) {
	if !volumeAction(input.Action) {
		return inventory.MutationTarget{}, nil
	}
	if vendor != "aws-ec2" && vendor != "openstack" && !publicCloudProvider(vendor) {
		return inventory.MutationTarget{}, ErrInvalid
	}
	if input.Action == "attach-volume" && vendor != provider.HuaweiECS &&
		((vendor == "aws-ec2") != (input.Device != "")) {
		return inventory.MutationTarget{}, ErrInvalid
	}
	var volume inventory.MutationTarget
	var err error
	if lock {
		volume, err = inventory.ResolveVolumeMutation(ctx, tx, actor, input.Account, input.Region, input.VolumeID)
	} else {
		volume, err = inventory.ResolveMutationPreview(ctx, tx, actor, input.Account, input.Region, input.VolumeID, provider.Volume)
	}
	if err != nil {
		return volume, err
	}
	if volume.OwnershipRevision != input.VolumeRevision {
		return volume, ErrVolumeStale
	}
	var snapshotPending bool
	err = tx.QueryRowContext(ctx, `SELECT EXISTS (
		SELECT 1 FROM cmp_network_intents AS intent JOIN cmp_instance_operations AS op USING(operation_id)
		WHERE (intent.parent_scope_id,intent.parent_id)=($1,$2) AND op.action IN ('create-snapshot','rollback-snapshot')
		  AND op.status IN ('awaiting_approval','queued','applying','observing','needs_review')
	)`+" OR "+retirementDiskReserved+" OR "+volumeChangeConflict, volume.ScopeID, input.VolumeID, "").Scan(&snapshotPending)
	if err == nil && snapshotPending {
		return volume, ErrConflict
	}
	return volume, err
}

func (s Store) processVolume(ctx context.Context, task mq.Task, w work, adapter provider.InstanceOperator, scope provider.Scope) (bool, error) {
	operator, ok := adapter.(provider.VolumeOperator)
	if !ok {
		return false, s.endUnavailable(ctx, task, w)
	}
	change := provider.VolumeChange{InstanceID: w.AssetID, VolumeID: w.VolumeID, Device: w.Device, Attach: w.Action == "attach-volume"}
	if w.Status == "queued" {
		return s.dispatchVolume(ctx, task, w, operator, scope, change)
	}
	if w.Status == "applying" {
		if err := s.transition(ctx, task, "applying", "observing", "submission_outcome_unknown", ""); err != nil {
			return false, err
		}
		w.Status = "observing"
	}
	reached, err := operator.ObserveVolume(ctx, scope, change)
	if err == nil && reached {
		return false, s.confirmVolume(ctx, task, w, change.Attach)
	}
	if task.Attempts >= task.MaxAttempts || provider.IsFailure(err, provider.InvalidResponse) || provider.IsFailure(err, provider.Unauthorized) {
		return false, s.transition(ctx, task, w.Status, "needs_review", "volume_completion_requires_evidence", "")
	}
	return true, nil
}

func (s Store) dispatchVolume(ctx context.Context, task mq.Task, w work, operator provider.VolumeOperator, scope provider.Scope, change provider.VolumeChange) (bool, error) {
	send := func(ctx context.Context) (string, error) {
		return operator.ApplyVolume(ctx, scope, change)
	}
	if preparer, ok := operator.(provider.VolumePreparer); ok {
		prepared, err := preparer.PrepareVolume(ctx, scope, change)
		if err != nil || prepared == nil || ctx.Err() != nil {
			if task.Attempts < task.MaxAttempts && (ctx.Err() != nil || remotePreflightRetryable(err)) {
				return true, nil
			}
			cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), accountingTimeout)
			defer cancel()
			return false, s.transition(cleanup, task, w.Status, "failed", "provider_volume_precondition", "")
		}
		send = prepared
	}
	armed, err := s.prepare(ctx, task, true)
	if errors.Is(err, identity.ErrForbidden) || errors.Is(err, identity.ErrAccountStale) || errors.Is(err, ErrConflict) {
		return false, s.endUnavailable(ctx, task, armed)
	}
	if err != nil {
		return false, err
	}
	if armed.Status != "applying" {
		return false, mq.ErrLeaseLost
	}
	requestID, callErr := send(ctx)
	state, reason := "observing", "provider_accepted"
	switch {
	case provider.IsFailure(callErr, provider.VolumeBlocked):
		state, reason = "failed", "provider_volume_precondition"
	case callErr != nil:
		reason = "submission_outcome_unknown"
	}
	cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), accountingTimeout)
	defer cancel()
	return !terminal(state), s.transition(cleanup, task, "applying", state, reason, requestID)
}

func (s Store) confirmVolume(ctx context.Context, task mq.Task, w work, attached bool) error {
	err := s.withLease(ctx, task, func(tx *sql.Tx) error {
		if err := inventory.LockVolumeScopes(ctx, tx, w.actor, w.Account, w.Region); err != nil {
			return err
		}
		if err := checkOperationTarget(ctx, tx, &w); err != nil {
			return err
		}
		if err := inventory.RecordVolumeLink(ctx, tx, w.actor, w.Account, w.Region, w.AssetID, w.VolumeID, attached); err != nil {
			return err
		}
		return writeTransition(ctx, tx, task.ID, w.Status, "succeeded", "volume_relationship_confirmed", "")
	})
	if errors.Is(err, identity.ErrForbidden) || errors.Is(err, identity.ErrAccountStale) {
		return s.endUnavailable(ctx, task, w)
	}
	return err
}
