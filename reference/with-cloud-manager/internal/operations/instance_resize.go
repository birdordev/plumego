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

const InstanceResizeMigration = "0161_instance_resize"

func MigrateInstanceResize(ctx context.Context, db *sql.DB) error {
	return dbschema.Apply(ctx, db, InstanceResizeMigration)
}

const resizeInstance = "resize-instance"

type InstanceResizeInput struct {
	Account         string `json:"account"`
	Region          string `json:"region"`
	AssetID         string `json:"asset_id"`
	CurrentType     string `json:"current_type"`
	TargetType      string `json:"target_type"`
	AcceptAutoStart bool   `json:"accept_auto_start"`
}

type InstanceResizeView struct {
	CurrentType       string `json:"current_type"`
	TargetType        string `json:"target_type"`
	AcceptAutoStart   bool   `json:"accept_auto_start"`
	OwnershipRevision int64  `json:"ownership_revision"`
}

func (s Store) SubmitInstanceResize(ctx context.Context, token string, scope identity.Scope, key string, revision int64, input InstanceResizeInput) (Operation, error) {
	change := provider.InstanceResize{ID: input.AssetID, CurrentType: input.CurrentType, TargetType: input.TargetType, AcceptAutoStart: input.AcceptAutoStart}
	if !change.Valid() {
		return Operation{}, ErrInvalid
	}
	data, err := json.Marshal(struct {
		InstanceResizeInput
		Revision int64
	}{input, revision})
	if err != nil {
		return Operation{}, err
	}
	digest := sha256.Sum256(append([]byte(resizeInstance+"\x00"), data...))
	return s.submitApprovedAssetChange(ctx, token, scope, key, revision, provider.Instance,
		input.Account, input.Region, input.AssetID, resizeInstance, "", digest,
		func(ctx context.Context, tx *sql.Tx, _ identity.Scope, target inventory.MutationTarget) (func(string) error, error) {
			if !change.ValidFor(target.Provider) {
				return nil, ErrInvalid
			}
			if err := checkInstanceResizeTarget(ctx, tx, target, change, true); err != nil {
				return nil, err
			}
			return func(id string) error {
				_, err := tx.ExecContext(ctx, `INSERT INTO cmp_instance_resize_intents(operation_id,current_type,target_type,accept_auto_start)
					VALUES ($1,$2,$3,$4)`, id, change.CurrentType, change.TargetType, change.AcceptAutoStart)
				return err
			}, nil
		})
}

func checkInstanceResizeTarget(ctx context.Context, tx *sql.Tx, target inventory.MutationTarget, change provider.InstanceResize, beforeWrite bool) error {
	size, state, err := inventory.InstanceResizeTarget(ctx, tx, target, change.ID)
	if err != nil {
		return err
	}
	if size != change.CurrentType && (beforeWrite || size != change.TargetType) {
		return identity.ErrAccountStale
	}
	if !beforeWrite {
		return nil
	}
	if state != provider.InstanceStoppedState(target.Provider) {
		return ErrConflict
	}
	busy, err := inventory.InstanceImageCreationActive(ctx, tx, target.ScopeID, change.ID, "")
	if err == nil && busy {
		return ErrConflict
	}
	return err
}

func checkInstanceResize(ctx context.Context, tx *sql.Tx, w *work) error {
	var scopeID int64
	var observer creationObserver
	change := &w.instanceResize
	change.ID = w.AssetID
	err := tx.QueryRowContext(ctx, `SELECT o.scope_id,i.current_type,i.target_type,i.accept_auto_start,
		i.observation_reviewer,i.observation_membership_revision,i.observation_credential_revision
		FROM cmp_instance_resize_intents AS i JOIN cmp_instance_operations AS o USING(operation_id)
		WHERE operation_id=$1`, w.ID).Scan(&scopeID, &change.CurrentType, &change.TargetType, &change.AcceptAutoStart,
		&observer.principal, &observer.membership, &observer.credential)
	if err != nil {
		return err
	}
	target, beforeWrite, err := resolveApprovedAssetChange(ctx, tx, w, scopeID, observer, provider.Instance)
	if err != nil {
		return err
	}
	if !change.ValidFor(target.Provider) {
		return ErrConflict
	}
	return checkInstanceResizeTarget(ctx, tx, target, *change, beforeWrite)
}

func (s Store) processInstanceResize(ctx context.Context, task mq.Task, w work, client provider.InstanceOperator, scope provider.Scope) (bool, error) {
	resizer, ok := client.(provider.InstanceResizer)
	if !ok {
		return false, s.endUnavailable(ctx, task, w)
	}
	if w.Status == "queued" {
		apply, err := resizer.PrepareInstanceResize(ctx, scope, w.instanceResize)
		return s.dispatchSingleMutation(ctx, task, w, apply, err, "instance_resize")
	}
	if w.Status == "applying" {
		if err := s.transition(ctx, task, "applying", "observing", "submission_outcome_unknown", ""); err != nil {
			return false, err
		}
		w.Status = "observing"
	}
	result, state, err := resizer.ObserveInstanceResize(ctx, scope, w.instanceResize, w.ProviderRequestID)
	expected := provider.InstanceStoppedState(scope.Provider)
	if w.instanceResize.AcceptAutoStart {
		expected = "RUNNING"
	}
	if err == nil && result == provider.ActionSucceeded && state == expected {
		return false, s.confirmInstanceResize(ctx, task, w, state)
	}
	if result == provider.ActionFailed || task.Attempts >= task.MaxAttempts || provider.IsFailure(err, provider.InvalidResponse) ||
		provider.IsFailure(err, provider.Unauthorized) || provider.IsFailure(err, provider.Unsupported) {
		return false, s.transition(ctx, task, w.Status, "needs_review", "instance_resize_requires_evidence", "")
	}
	return true, nil
}

func (s Store) confirmInstanceResize(ctx context.Context, task mq.Task, w work, observed string) error {
	err := s.withLease(ctx, task, func(tx *sql.Tx) error {
		scopeID, err := inventory.LockInstanceScope(ctx, tx, w.actor.Tenant, w.Account, w.Region)
		if err != nil {
			return err
		}
		if err := checkInstanceResize(ctx, tx, &w); err != nil {
			return err
		}
		if err := inventory.RecordObservedInstanceType(ctx, tx, scopeID, w.instanceResize, observed); err != nil {
			return err
		}
		return writeTransition(ctx, tx, w.ID, w.Status, "succeeded", "instance_type_observed", "")
	})
	if errors.Is(err, identity.ErrForbidden) || errors.Is(err, identity.ErrAccountStale) || errors.Is(err, ErrConflict) {
		return s.endUnavailable(ctx, task, w)
	}
	return err
}
