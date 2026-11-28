package operations

import (
	"context"
	"crypto/sha256"
	"database/sql"
	_ "embed"
	"encoding/json"
	"errors"
	"time"

	"github.com/birdordev/plumego/x/messaging/mq"

	"with-cloud-manager/internal/dbschema"
	"with-cloud-manager/internal/identity"
	"with-cloud-manager/internal/inventory"
	"with-cloud-manager/internal/provider"
)

//go:embed 0163_bucket_versioning_change.sql
var bucketVersioningSchema string

const BucketVersioningMigration = "0163_bucket_versioning_change"
const setBucketVersioning = "set-bucket-versioning"

func MigrateBucketVersioning(ctx context.Context, db *sql.DB) error {
	return dbschema.Apply(ctx, db, BucketVersioningMigration, bucketVersioningSchema)
}

type BucketVersioningInput struct {
	Account string                    `json:"account"`
	AssetID string                    `json:"asset_id"`
	Current provider.BucketVersioning `json:"current_state"`
	Target  provider.BucketVersioning `json:"target_state"`
}

type BucketVersioningView struct {
	Location string                    `json:"location"`
	Current  provider.BucketVersioning `json:"current_state"`
	Target   provider.BucketVersioning `json:"target_state"`
}

func (s Store) SubmitBucketVersioning(ctx context.Context, token string, scope identity.Scope, key string, revision int64, input BucketVersioningInput) (Operation, error) {
	change := provider.BucketVersioningChange{ID: input.AssetID, Current: input.Current, Target: input.Target}
	if !change.Valid() {
		return Operation{}, ErrInvalid
	}
	data, err := json.Marshal(struct {
		BucketVersioningInput
		Revision int64
	}{input, revision})
	if err != nil {
		return Operation{}, err
	}
	digest := sha256.Sum256(append([]byte(setBucketVersioning+"\x00"), data...))
	return s.submitApprovedAssetChange(ctx, token, scope, key, revision, provider.ObjectBucket, input.Account,
		provider.ObjectBucketGlobalScope, input.AssetID, setBucketVersioning, "", digest,
		func(ctx context.Context, tx *sql.Tx, _ identity.Scope, target inventory.MutationTarget) (func(string) error, error) {
			if !publicCloudProvider(target.Provider) {
				return nil, ErrInvalid
			}
			location, state, err := inventory.BucketVersioningMutationTarget(ctx, tx, target, input.AssetID)
			if err != nil {
				return nil, err
			}
			if location == "" || state != input.Current {
				return nil, identity.ErrAccountStale
			}
			return func(id string) error {
				_, err := tx.ExecContext(ctx, `INSERT INTO cmp_bucket_versioning_intents(operation_id,location,current_state,target_state)
					VALUES ($1,$2,$3,$4)`, id, location, input.Current, input.Target)
				return err
			}, nil
		})
}

func checkBucketVersioning(ctx context.Context, tx *sql.Tx, w *work) (inventory.MutationTarget, error) {
	var scopeID int64
	var observer creationObserver
	change := &w.bucketVersioning
	change.ID = w.AssetID
	err := tx.QueryRowContext(ctx, `SELECT o.scope_id,i.location,i.current_state,i.target_state,
		i.observation_reviewer,i.observation_membership_revision,i.observation_credential_revision
		FROM cmp_bucket_versioning_intents AS i JOIN cmp_instance_operations AS o USING(operation_id) WHERE operation_id=$1`, w.ID).
		Scan(&scopeID, &change.Location, &change.Current, &change.Target, &observer.principal, &observer.membership, &observer.credential)
	if err != nil {
		return inventory.MutationTarget{}, err
	}
	target, beforeWrite, err := resolveApprovedAssetChange(ctx, tx, w, scopeID, observer, provider.ObjectBucket)
	if err != nil {
		return target, err
	}
	location, state, err := inventory.BucketVersioningMutationTarget(ctx, tx, target, change.ID)
	if err == nil && (location != change.Location || (beforeWrite && state != change.Current)) {
		err = identity.ErrAccountStale
	}
	return target, err
}

func (s Store) processBucketVersioning(ctx context.Context, task mq.Task, w work, client provider.InstanceOperator, scope provider.Scope) (bool, error) {
	operator, ok := client.(provider.BucketVersioningOperator)
	if !ok {
		return false, s.endUnavailable(ctx, task, w)
	}
	scope.Kind = provider.ObjectBucket
	if w.Status == "queued" {
		apply, err := operator.PrepareBucketVersioningChange(ctx, scope, w.bucketVersioning)
		return s.dispatchSingleMutation(ctx, task, w, apply, err, "bucket_versioning")
	}
	if w.Status == "applying" {
		if err := s.transition(ctx, task, "applying", "observing", "submission_outcome_unknown", ""); err != nil {
			return false, err
		}
		w.Status = "observing"
	}
	started, err := s.dbTime(ctx)
	if err != nil {
		return false, err
	}
	state, err := operator.ReadBucketVersioning(ctx, scope, w.AssetID, w.bucketVersioning.Location)
	if err == nil && state == w.bucketVersioning.Target {
		return false, s.confirmBucketVersioning(ctx, task, w, started)
	}
	if task.Attempts >= task.MaxAttempts || provider.IsFailure(err, provider.InvalidResponse) ||
		provider.IsFailure(err, provider.Unauthorized) || provider.IsFailure(err, provider.Unsupported) ||
		provider.IsFailure(err, provider.Absent) {
		return false, s.transition(ctx, task, w.Status, "needs_review", "bucket_versioning_requires_evidence", "")
	}
	return true, nil
}

func (s Store) confirmBucketVersioning(ctx context.Context, task mq.Task, w work, started time.Time) error {
	err := s.withLease(ctx, task, func(tx *sql.Tx) error {
		target, err := checkBucketVersioning(ctx, tx, &w)
		if err != nil {
			return err
		}
		if err := inventory.RecordObservedBucketVersioning(ctx, tx, target, w.bucketVersioning, started); err != nil {
			return err
		}
		return writeTransition(ctx, tx, w.ID, w.Status, "succeeded", "bucket_versioning_observed", "")
	})
	if errors.Is(err, identity.ErrForbidden) || errors.Is(err, identity.ErrAccountStale) || errors.Is(err, ErrConflict) {
		return s.endUnavailable(ctx, task, w)
	}
	return err
}
