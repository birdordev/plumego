package inventory

import (
	"context"
	"database/sql"
	"errors"
	"time"

	dbutil "github.com/birdordev/plumego/store/db"

	"with-cloud-manager/internal/provider"
)

const BucketVersioningMigration = "0143_bucket_versioning"
const BucketEncryptionMigration = "0144_bucket_encryption"

var ErrBucketSettingNotFound = errors.New("object bucket setting unavailable")

type BucketSettingQuery struct {
	Tenant, Project, Account, ID string
}

type bucketSettingValue interface {
	~string
	Valid() bool
}

type bucketSettingSnapshot[T bucketSettingValue] struct {
	State      T         `json:"state"`
	ObservedAt time.Time `json:"observed_at"`
}

type BucketVersioningSnapshot bucketSettingSnapshot[provider.BucketVersioning]
type BucketEncryptionSnapshot bucketSettingSnapshot[provider.BucketEncryption]

const bucketVersioningTable = "cmp_bucket_versioning"
const bucketEncryptionTable = "cmp_bucket_encryption"

const currentObjectBucket = `s.kind='object-bucket' AND s.region='global' AND o.object_bucket IS NOT NULL
	AND o.removed_at IS NULL AND o.missing_generation IS NULL
	AND a.mode IN ('read_only','write_enabled') AND a.credential_revision=s.credential_revision
	AND a.remote_identity<>''`

const selectVersionedBucket = ` FROM cmp_inventory_objects AS o
	JOIN cmp_inventory_scopes AS s USING (scope_id)
	JOIN cmp_cloud_accounts AS a USING (tenant_id,account_id)
	WHERE (s.tenant_id,s.account_id,o.external_id,a.credential_revision)=($1,$2,$3,$4) AND ` + currentObjectBucket

type bucketSettingTarget struct {
	scopeID        int64
	scope          provider.Scope
	id, location   string
	parentObserved time.Time
	started        time.Time
}

func (s Store) RefreshBucketVersioning(ctx context.Context, key Key, id string, revision int64, reader provider.BucketVersioningReader) error {
	return refreshBucketSetting(ctx, s, key, id, revision, bucketVersioningTable, reader.ReadBucketVersioning)
}

func (s Store) RefreshBucketEncryption(ctx context.Context, key Key, id string, revision int64, reader provider.BucketEncryptionReader) error {
	return refreshBucketSetting(ctx, s, key, id, revision, bucketEncryptionTable, reader.ReadBucketEncryption)
}

func refreshBucketSetting[T bucketSettingValue](ctx context.Context, s Store, key Key, id string, revision int64, table string,
	read func(context.Context, provider.Scope, string, string) (T, error),
) error {
	if key.Kind != provider.ObjectBucket || key.Region != provider.ObjectBucketGlobalScope || revision <= 0 ||
		!validConfigurationQueryValue(key.Tenant, 256) || !validConfigurationQueryValue(key.Account, 256) || !provider.ValidBucketAssetID(id) {
		return ErrInvalidQuery
	}
	target := bucketSettingTarget{id: id, scope: provider.Scope{Kind: key.Kind, Region: key.Region}}
	err := s.db.QueryRowContext(ctx, `SELECT s.scope_id,a.provider,a.remote_identity,o.object_bucket->>'location',o.observed_at,clock_timestamp()`+
		selectVersionedBucket, key.Tenant, key.Account, id, revision).Scan(&target.scopeID, &target.scope.Provider,
		&target.scope.RemoteIdentity, &target.location, &target.parentObserved, &target.started)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrStale
	}
	if err != nil {
		return err
	}
	publish, err := collectBucketSetting(ctx, key, revision, table, target, read)
	if err != nil {
		return err
	}
	return dbutil.WithTransaction(ctx, s.db, nil, func(tx *sql.Tx) error { return publish(ctx, tx) })
}

func collectBucketSetting[T bucketSettingValue](ctx context.Context, key Key, revision int64, table string,
	target bucketSettingTarget, read func(context.Context, provider.Scope, string, string) (T, error),
) (func(context.Context, *sql.Tx) error, error) {
	state, err := read(ctx, target.scope, target.id, target.location)
	if err != nil {
		return nil, err
	}
	if !state.Valid() {
		return nil, &provider.Failure{Kind: provider.InvalidResponse}
	}
	return publishBucketSetting(key, revision, table, target, state), nil
}

func publishBucketSetting[T bucketSettingValue](key Key, revision int64, table string, target bucketSettingTarget, state T) func(context.Context, *sql.Tx) error {
	return func(ctx context.Context, tx *sql.Tx) error {
		var found int64
		err := tx.QueryRowContext(ctx, "SELECT s.scope_id"+selectVersionedBucket+
			" AND s.scope_id=$5 AND o.observed_at=$6 AND a.remote_identity=$7 AND o.object_bucket->>'location'=$8 FOR SHARE OF a,s,o",
			key.Tenant, key.Account, target.id, revision, target.scopeID, target.parentObserved, target.scope.RemoteIdentity, target.location).Scan(&found)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrStale
		}
		if err != nil {
			return err
		}
		updated, err := upsertBucketSetting(ctx, tx, table, target.scopeID, target.id, revision, target.location, state, target.started)
		if err == nil && !updated {
			return ErrStale
		}
		return err
	}
}

func upsertBucketSetting[T bucketSettingValue](ctx context.Context, tx *sql.Tx, table string,
	scopeID int64, id string, revision int64, location string, state T, started time.Time,
) (bool, error) {
	result, err := tx.ExecContext(ctx, `INSERT INTO `+table+` AS previous
		(scope_id,external_id,credential_revision,location,state,refresh_started_at,observed_at)
		VALUES ($1,$2,$3,$4,$5,$6,clock_timestamp())
		ON CONFLICT (scope_id,external_id) DO UPDATE SET credential_revision=EXCLUDED.credential_revision,
			location=EXCLUDED.location,state=EXCLUDED.state,refresh_started_at=EXCLUDED.refresh_started_at,observed_at=EXCLUDED.observed_at
		WHERE (previous.credential_revision,previous.refresh_started_at) < (EXCLUDED.credential_revision,EXCLUDED.refresh_started_at)`,
		scopeID, id, revision, location, state, started)
	if err != nil {
		return false, err
	}
	updated, err := result.RowsAffected()
	return updated == 1, err
}

func (s Store) ReadBucketVersioning(ctx context.Context, token string, query BucketSettingQuery) (BucketVersioningSnapshot, error) {
	snapshot, err := readBucketSetting[provider.BucketVersioning](ctx, s, token, query, bucketVersioningTable)
	return BucketVersioningSnapshot(snapshot), err
}

func (s Store) ReadBucketEncryption(ctx context.Context, token string, query BucketSettingQuery) (BucketEncryptionSnapshot, error) {
	snapshot, err := readBucketSetting[provider.BucketEncryption](ctx, s, token, query, bucketEncryptionTable)
	return BucketEncryptionSnapshot(snapshot), err
}

func readBucketSetting[T bucketSettingValue](ctx context.Context, s Store, token string, query BucketSettingQuery, table string) (bucketSettingSnapshot[T], error) {
	if !validConfigurationQueryValue(query.Tenant, 256) || !validConfigurationQueryValue(query.Project, 256) ||
		!validConfigurationQueryValue(query.Account, 256) || !provider.ValidBucketAssetID(query.ID) {
		return bucketSettingSnapshot[T]{}, ErrInvalidQuery
	}
	var result bucketSettingSnapshot[T]
	err := s.readProject(ctx, token, Query{Tenant: query.Tenant, Project: query.Project}, func(tx *sql.Tx) error {
		err := tx.QueryRowContext(ctx, `SELECT snapshot.state,snapshot.observed_at
			FROM cmp_inventory_scopes AS s JOIN cmp_cloud_accounts AS a USING (tenant_id,account_id)
			JOIN cmp_inventory_objects AS o USING (scope_id)
			JOIN `+table+` AS snapshot ON (snapshot.scope_id,snapshot.external_id)=(s.scope_id,o.external_id)
				AND snapshot.credential_revision=a.credential_revision AND snapshot.location=o.object_bucket->>'location'`+inventoryOwnershipJoins+
			" WHERE "+assetVisibility+" AND "+currentObjectBucket+" AND (s.account_id,o.external_id)=($3,$4)",
			query.Tenant, query.Project, query.Account, query.ID).Scan(&result.State, &result.ObservedAt)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrBucketSettingNotFound
		}
		return err
	})
	return result, err
}
