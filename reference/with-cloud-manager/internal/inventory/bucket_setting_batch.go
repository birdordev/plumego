package inventory

import (
	"context"
	"database/sql"
	"errors"

	"with-cloud-manager/internal/provider"
)

type BucketSettingBatch struct {
	Next    string
	Publish func(context.Context, *sql.Tx) (int, error)
}

func (s Store) PrepareBucketEncryptionBatch(ctx context.Context, key Key, revision int64, after string,
	read func(context.Context, provider.Scope, []provider.BucketSettingTarget) ([]provider.BucketSettingResult[provider.BucketEncryption], error),
) (BucketSettingBatch, error) {
	return prepareBucketSettingBatch(ctx, s, key, revision, after, bucketEncryptionTable, read)
}

func (s Store) PrepareBucketVersioningBatch(ctx context.Context, key Key, revision int64, after string,
	read func(context.Context, provider.Scope, []provider.BucketSettingTarget) ([]provider.BucketSettingResult[provider.BucketVersioning], error),
) (BucketSettingBatch, error) {
	return prepareBucketSettingBatch(ctx, s, key, revision, after, bucketVersioningTable, read)
}

func prepareBucketSettingBatch[T bucketSettingValue](ctx context.Context, s Store, key Key, revision int64, after, table string,
	read func(context.Context, provider.Scope, []provider.BucketSettingTarget) ([]provider.BucketSettingResult[T], error),
) (BucketSettingBatch, error) {
	if key.Kind != provider.ObjectBucket || key.Region != provider.ObjectBucketGlobalScope || revision <= 0 ||
		(after != "" && !provider.ValidBucketAssetID(after)) {
		return BucketSettingBatch{}, ErrInvalidQuery
	}
	targets, err := s.bucketSettingTargets(ctx, key, revision, after)
	if err != nil {
		return BucketSettingBatch{}, err
	}
	var results []provider.BucketSettingResult[T]
	var readErr error
	if len(targets) > 0 {
		targetsToRead := make([]provider.BucketSettingTarget, min(len(targets), provider.BucketSettingBatchLimit))
		for i := range targetsToRead {
			targetsToRead[i] = provider.BucketSettingTarget{ID: targets[i].id, Location: targets[i].location}
		}
		results, readErr = read(ctx, targets[0].scope, targetsToRead)
		throttled := provider.IsFailure(readErr, provider.Throttled)
		if readErr != nil && (!throttled || len(results) == 0) {
			return BucketSettingBatch{}, readErr
		}
		if len(results) > len(targetsToRead) || (len(results) != len(targetsToRead) && !throttled) {
			return BucketSettingBatch{}, &provider.Failure{Kind: provider.InvalidResponse}
		}
	}
	var next string
	if len(results) < len(targets) {
		next = targets[len(results)-1].id
	}
	return BucketSettingBatch{
		Next: next,
		Publish: func(ctx context.Context, tx *sql.Tx) (int, error) {
			var rejected int
			for i, result := range results {
				if result.Err != nil || !result.State.Valid() {
					rejected++
					continue
				}
				err := publishBucketSetting(key, revision, table, targets[i], result.State)(ctx, tx)
				if errors.Is(err, ErrStale) {
					rejected++
					continue
				}
				if err != nil {
					return 0, err
				}
			}
			return rejected, nil
		},
	}, readErr
}

func (s Store) bucketSettingTargets(ctx context.Context, key Key, revision int64, after string) ([]bucketSettingTarget, error) {
	var scopeID int64
	err := s.db.QueryRowContext(ctx, `SELECT scope_id FROM cmp_inventory_scopes
		WHERE (tenant_id,account_id,region,kind)=($1,$2,$3,$4)
		AND credential_revision=$5 AND completed_credential_revision=$5`,
		key.Tenant, key.Account, key.Region, key.Kind, revision).Scan(&scopeID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrStale
	}
	if err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT s.scope_id,a.provider,a.remote_identity,o.external_id,
		o.object_bucket->>'location',o.observed_at,statement_timestamp()
		FROM cmp_inventory_scopes AS s
		JOIN cmp_cloud_accounts AS a USING (tenant_id,account_id)
		JOIN cmp_inventory_objects AS o USING (scope_id)
		WHERE s.scope_id=$1 AND s.completed_credential_revision=$2 AND a.credential_revision=$2 AND o.external_id>$3 AND `+
		currentObjectBucket+` ORDER BY o.external_id LIMIT $4`, scopeID, revision, after, provider.BucketSettingBatchLimit+1)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	targets := make([]bucketSettingTarget, 0, provider.BucketSettingBatchLimit+1)
	for rows.Next() {
		target := bucketSettingTarget{scope: provider.Scope{Kind: key.Kind, Region: key.Region}}
		if err := rows.Scan(&target.scopeID, &target.scope.Provider, &target.scope.RemoteIdentity,
			&target.id, &target.location, &target.parentObserved, &target.started); err != nil {
			return nil, err
		}
		targets = append(targets, target)
	}
	return targets, rows.Err()
}
