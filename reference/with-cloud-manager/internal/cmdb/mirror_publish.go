package cmdb

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"

	"with-cloud-manager/internal/inventory"
)

const stageBatchSize = 256
const stageBatchBytes = 4 << 20

func stageSnapshot(ctx context.Context, tx *sql.Tx, project Project, input io.Reader) (Manifest, Summary, error) {
	if _, err := tx.ExecContext(ctx, `CREATE TEMP TABLE cmp_cmdb_stage (
		account_id text NOT NULL, region text NOT NULL, kind text NOT NULL, asset_id text NOT NULL, asset jsonb NOT NULL,
		PRIMARY KEY (account_id,region,kind,asset_id)) ON COMMIT DROP`); err != nil {
		return Manifest{}, Summary{}, err
	}
	batch := make([]inventory.CMDBAsset, 0, stageBatchSize)
	batchBytes := 0
	flush := func() error {
		if len(batch) == 0 {
			return nil
		}
		data, err := json.Marshal(batch)
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO pg_temp.cmp_cmdb_stage (account_id,region,kind,asset_id,asset)
			SELECT asset->>'account',asset->>'region',asset->>'kind',asset->>'id',asset
			FROM jsonb_array_elements($1::jsonb) AS rows(asset)`, string(data))
		var state interface{ SQLState() string }
		if errors.As(err, &state) && state.SQLState() == "23505" {
			return ErrInvalidSnapshot
		}
		clear(batch)
		batch = batch[:0]
		batchBytes = 0
		return err
	}
	manifest, summary, err := readSnapshot(input, project, func(asset inventory.CMDBAsset, size int) error {
		if batchBytes+size > stageBatchBytes {
			if err := flush(); err != nil {
				return err
			}
		}
		batch = append(batch, asset)
		batchBytes += size
		if len(batch) == stageBatchSize {
			return flush()
		}
		return nil
	})
	if err != nil {
		return manifest, summary, err
	}
	return manifest, summary, flush()
}

func publishSnapshot(ctx context.Context, tx *sql.Tx, key MirrorKey, receipt *Receipt) error {
	params := []any{key.Source, key.Scope.Tenant, key.Scope.Project}
	var generation int64
	err := tx.QueryRowContext(ctx, `UPDATE cmp_cmdb_mirrors SET published_generation=$4,captured_at=$5
		WHERE (source_url,tenant_id,project_id)=($1,$2,$3) AND issued_generation=$4 AND published_generation<$4
			AND (captured_at IS NULL OR captured_at<=$5) AND $5<=clock_timestamp()+interval '5 minutes'
		RETURNING published_generation`, append(params, receipt.Generation, receipt.CapturedAt)...).Scan(&generation)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrStaleSnapshot
	}
	if err != nil {
		return err
	}
	// 仅更新内容有差异的行，完整快照中的缺项才会从镜像移除。
	updated, err := tx.ExecContext(ctx, `INSERT INTO cmp_cmdb_assets AS mirrored
		(source_url,tenant_id,project_id,account_id,region,kind,asset_id,asset)
		SELECT $1,$2,$3,stage.account_id,stage.region,stage.kind,stage.asset_id,stage.asset
		FROM pg_temp.cmp_cmdb_stage AS stage
		LEFT JOIN cmp_cmdb_assets AS old ON (old.source_url,old.tenant_id,old.project_id) = ($1,$2,$3)
			AND (old.account_id,old.region,old.kind,old.asset_id)=(stage.account_id,stage.region,stage.kind,stage.asset_id)
		WHERE old.asset IS DISTINCT FROM stage.asset
		ON CONFLICT (source_url,tenant_id,project_id,account_id,region,kind,asset_id)
		DO UPDATE SET asset=EXCLUDED.asset WHERE mirrored.asset IS DISTINCT FROM EXCLUDED.asset`, params...)
	if err != nil {
		return err
	}
	if receipt.Upserted, err = updated.RowsAffected(); err != nil {
		return err
	}
	removed, err := tx.ExecContext(ctx, `DELETE FROM cmp_cmdb_assets AS mirrored WHERE
		(mirrored.source_url,mirrored.tenant_id,mirrored.project_id)=($1,$2,$3) AND NOT EXISTS (
			SELECT 1 FROM pg_temp.cmp_cmdb_stage AS stage WHERE
			(stage.account_id,stage.region,stage.kind,stage.asset_id)=(mirrored.account_id,mirrored.region,mirrored.kind,mirrored.asset_id))`, params...)
	if err != nil {
		return err
	}
	if receipt.Removed, err = removed.RowsAffected(); err != nil {
		return err
	}
	return tx.QueryRowContext(ctx, `INSERT INTO cmp_cmdb_reconciliations
		(source_url,tenant_id,project_id,generation,captured_at,asset_count,upserted,removed,digest)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9) RETURNING published_at`,
		key.Source, key.Scope.Tenant, key.Scope.Project, generation, receipt.CapturedAt,
		receipt.Count, receipt.Upserted, receipt.Removed, receipt.SHA256).Scan(&receipt.PublishedAt)
}
