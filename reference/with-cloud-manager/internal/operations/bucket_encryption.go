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

const BucketEncryptionEnableMigration = "0164_bucket_encryption_enable"
const enableBucketEncryption = "enable-bucket-encryption"

func MigrateBucketEncryptionEnable(ctx context.Context, db *sql.DB) error {
	return dbschema.Apply(ctx, db, BucketEncryptionEnableMigration)
}

type BucketEncryptionInput struct {
	Account string `json:"account"`
	AssetID string `json:"asset_id"`
}

type BucketEncryptionView struct {
	Location string                    `json:"location"`
	Current  provider.BucketEncryption `json:"current_state"`
	Target   provider.BucketEncryption `json:"target_state"`
}

func (s Store) SubmitBucketEncryption(ctx context.Context, token string, scope identity.Scope, key string, revision int64, input BucketEncryptionInput) (Operation, error) {
	if !provider.ValidBucketSettingAssetID(input.AssetID) {
		return Operation{}, ErrInvalid
	}
	data, err := json.Marshal(struct {
		BucketEncryptionInput
		Revision int64
	}{input, revision})
	if err != nil {
		return Operation{}, err
	}
	digest := sha256.Sum256(append([]byte(enableBucketEncryption+"\x00"), data...))
	return s.submitApprovedAssetChange(ctx, token, scope, key, revision, provider.ObjectBucket, input.Account,
		provider.ObjectBucketGlobalScope, input.AssetID, enableBucketEncryption, "", digest,
		func(ctx context.Context, tx *sql.Tx, _ identity.Scope, target inventory.MutationTarget) (func(string) error, error) {
			if !publicCloudProvider(target.Provider) {
				return nil, ErrInvalid
			}
			location, state, err := inventory.BucketEncryptionMutationTarget(ctx, tx, target, input.AssetID)
			if err != nil {
				return nil, err
			}
			if location == "" || state != provider.BucketEncryptionUnconfigured {
				return nil, identity.ErrAccountStale
			}
			return func(id string) error {
				_, err := tx.ExecContext(ctx, `INSERT INTO cmp_bucket_encryption_intents(operation_id,location) VALUES ($1,$2)`, id, location)
				return err
			}, nil
		})
}

func checkBucketEncryption(ctx context.Context, tx *sql.Tx, w *work) (inventory.MutationTarget, error) {
	var scopeID int64
	var observer creationObserver
	err := tx.QueryRowContext(ctx, `SELECT o.scope_id,i.location,i.observation_reviewer,i.observation_membership_revision,i.observation_credential_revision
		FROM cmp_bucket_encryption_intents AS i JOIN cmp_instance_operations AS o USING(operation_id) WHERE operation_id=$1`, w.ID).
		Scan(&scopeID, &w.bucketEncryptionLocation, &observer.principal, &observer.membership, &observer.credential)
	if err != nil {
		return inventory.MutationTarget{}, err
	}
	target, beforeWrite, err := resolveApprovedAssetChange(ctx, tx, w, scopeID, observer, provider.ObjectBucket)
	if err != nil {
		return target, err
	}
	location, state, err := inventory.BucketEncryptionMutationTarget(ctx, tx, target, w.AssetID)
	if err == nil && (location != w.bucketEncryptionLocation || (beforeWrite && state != provider.BucketEncryptionUnconfigured)) {
		err = identity.ErrAccountStale
	}
	return target, err
}

func (s Store) processBucketEncryption(ctx context.Context, task mq.Task, w work, client provider.InstanceOperator, scope provider.Scope) (bool, error) {
	operator, ok := client.(provider.BucketEncryptionOperator)
	if !ok {
		return false, s.endUnavailable(ctx, task, w)
	}
	scope.Kind = provider.ObjectBucket
	if w.Status == "queued" {
		apply, err := operator.PrepareBucketEncryptionEnable(ctx, scope, w.AssetID, w.bucketEncryptionLocation)
		return s.dispatchSingleMutation(ctx, task, w, apply, err, "bucket_encryption")
	}
	return s.observeBucketSetting(ctx, task, w, "bucket_encryption", func() (bool, error) {
		state, err := operator.ReadBucketEncryption(ctx, scope, w.AssetID, w.bucketEncryptionLocation)
		return state == provider.BucketEncryptionAES256, err
	}, func(observedWork work, started time.Time) error {
		return s.confirmBucketEncryption(ctx, task, observedWork, started)
	})
}

func (s Store) confirmBucketEncryption(ctx context.Context, task mq.Task, w work, started time.Time) error {
	err := s.withLease(ctx, task, func(tx *sql.Tx) error {
		target, err := checkBucketEncryption(ctx, tx, &w)
		if err != nil {
			return err
		}
		if err := inventory.RecordObservedBucketEncryption(ctx, tx, target, w.AssetID, w.bucketEncryptionLocation, started); err != nil {
			return err
		}
		return writeTransition(ctx, tx, w.ID, w.Status, "succeeded", "bucket_encryption_observed", "")
	})
	if errors.Is(err, identity.ErrForbidden) || errors.Is(err, identity.ErrAccountStale) || errors.Is(err, ErrConflict) {
		return s.endUnavailable(ctx, task, w)
	}
	return err
}
