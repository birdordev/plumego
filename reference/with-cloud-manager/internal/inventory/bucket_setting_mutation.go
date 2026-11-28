package inventory

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"with-cloud-manager/internal/identity"
	"with-cloud-manager/internal/provider"
)

func BucketVersioningMutationTarget(ctx context.Context, tx *sql.Tx, target MutationTarget, id string) (string, provider.BucketVersioning, error) {
	return bucketSettingMutationTarget[provider.BucketVersioning](ctx, tx, bucketVersioningTable, target, id)
}

func BucketEncryptionMutationTarget(ctx context.Context, tx *sql.Tx, target MutationTarget, id string) (string, provider.BucketEncryption, error) {
	return bucketSettingMutationTarget[provider.BucketEncryption](ctx, tx, bucketEncryptionTable, target, id)
}

func bucketSettingMutationTarget[T bucketSettingValue](ctx context.Context, tx *sql.Tx, table string, target MutationTarget, id string) (string, T, error) {
	var location string
	var state T
	err := tx.QueryRowContext(ctx, `SELECT o.object_bucket->>'location',COALESCE(v.state,'')
		FROM cmp_inventory_objects AS o JOIN cmp_inventory_scopes AS s USING (scope_id)
		LEFT JOIN `+table+` AS v ON (v.scope_id,v.external_id)=(o.scope_id,o.external_id)
			AND v.credential_revision=s.credential_revision AND v.location=o.object_bucket->>'location'
		WHERE (o.scope_id,o.external_id,s.credential_revision)=($1,$2,$3)
		AND s.kind='object-bucket' AND o.object_bucket IS NOT NULL`, target.ScopeID, id, target.CredentialRevision).Scan(&location, &state)
	if errors.Is(err, sql.ErrNoRows) {
		err = identity.ErrAccountStale
	}
	return location, state, err
}

func RecordObservedBucketVersioning(ctx context.Context, tx *sql.Tx, target MutationTarget, change provider.BucketVersioningChange, started time.Time) error {
	return recordObservedBucketSetting(ctx, tx, bucketVersioningTable, target, change.ID, change.Location, change.Target, started)
}

func RecordObservedBucketEncryption(ctx context.Context, tx *sql.Tx, target MutationTarget, id, location string, started time.Time) error {
	return recordObservedBucketSetting(ctx, tx, bucketEncryptionTable, target, id, location, provider.BucketEncryptionAES256, started)
}

func recordObservedBucketSetting[T bucketSettingValue](ctx context.Context, tx *sql.Tx, table string,
	target MutationTarget, id, location string, expected T, started time.Time,
) error {
	updated, err := upsertBucketSetting(ctx, tx, table, target.ScopeID, id, target.CredentialRevision, location, expected, started)
	if err != nil || updated {
		return err
	}
	actualLocation, state, err := bucketSettingMutationTarget[T](ctx, tx, table, target, id)
	if err == nil && (actualLocation != location || state != expected) {
		err = identity.ErrAccountStale
	}
	return err
}
