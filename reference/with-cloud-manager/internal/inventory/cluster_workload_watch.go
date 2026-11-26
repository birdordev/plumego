package inventory

import (
	"context"
	"database/sql"
	_ "embed"
	"encoding/json"
	"errors"
	"time"

	"with-cloud-manager/internal/provider"
)

//go:embed 0088_cluster_workload_checkpoints.sql
var workloadCheckpointSchema string

type workloadBaseline struct {
	snapshot    string
	started     time.Time
	observation provider.WorkloadObservation
}

// PrepareClusterWorkloadWatch 固定订阅的基线，提交时验证该基线和父集群仍有效。
func (s Store) PrepareClusterWorkloadWatch(ctx context.Context, key Key, id string, revision int64, binding [32]byte,
	observe func(context.Context, provider.WorkloadObservation) (provider.WorkloadObservation, error),
) (func(context.Context, *sql.Tx) error, error) {
	parent, err := s.currentClusterParent(ctx, key, id, revision)
	if err != nil {
		return nil, err
	}
	base, err := s.readWorkloadBaseline(ctx, parent.scopeID, id, revision, binding)
	if err != nil {
		return nil, err
	}
	observation, err := observe(ctx, base.observation)
	if err != nil {
		return nil, err
	}
	items, err := encodeClusterItems(observation.Items, workloadSnapshots.maxBytes, provider.NormalizeClusterWorkloads)
	if err != nil {
		return nil, err
	}
	versions, err := json.Marshal(observation.Versions)
	if err != nil {
		return nil, err
	}
	if len(observation.Versions) < 1 || len(observation.Versions) > 160 || len(versions) > 256<<10 {
		return nil, &provider.Failure{Kind: provider.InvalidResponse}
	}
	return func(ctx context.Context, tx *sql.Tx) error {
		if err := parent.lock(ctx, tx); err != nil {
			return err
		}
		args := []any{parent.scopeID, id, revision, parent.started, items, binding[:], string(versions)}
		if base.snapshot != "" {
			return requireClusterWrite(tx.ExecContext(ctx, updateWorkloadWatch, append(args, base.snapshot, base.started)...))
		}
		return requireClusterWrite(tx.ExecContext(ctx, insertWorkloadWatch, args...))
	}, nil
}

func (s Store) readWorkloadBaseline(ctx context.Context, scopeID int64, id string, revision int64, binding [32]byte) (workloadBaseline, error) {
	var base workloadBaseline
	var items, versions []byte
	err := s.db.QueryRowContext(ctx, `SELECT snapshot_id::text,refresh_started_at,
		CASE WHEN watch_snapshot_id=snapshot_id AND watch_binding=$4 THEN workloads END,
		CASE WHEN watch_snapshot_id=snapshot_id AND watch_binding=$4 THEN watch_versions END
		FROM cmp_cluster_workloads WHERE scope_id=$1 AND external_id=$2 AND credential_revision=$3`,
		scopeID, id, revision, binding[:]).Scan(&base.snapshot, &base.started, &items, &versions)
	if errors.Is(err, sql.ErrNoRows) {
		return base, nil
	}
	if err != nil || versions == nil {
		return base, err
	}
	if err := json.Unmarshal(items, &base.observation.Items); err != nil {
		return workloadBaseline{}, err
	}
	if err := json.Unmarshal(versions, &base.observation.Versions); err != nil {
		return workloadBaseline{}, err
	}
	return base, nil
}

// 内容相同时保留公开快照标识，后续分页可以继续读取这一版。
const updateWorkloadWatch = `WITH next AS (SELECT gen_random_uuid() AS snapshot_id)
	UPDATE cmp_cluster_workloads AS saved SET
		refresh_started_at=$4,
		observed_at=CASE WHEN saved.workloads=$5::jsonb THEN saved.observed_at ELSE clock_timestamp() END,
		snapshot_id=CASE WHEN saved.workloads=$5::jsonb THEN saved.snapshot_id ELSE next.snapshot_id END,
		watch_snapshot_id=CASE WHEN saved.workloads=$5::jsonb THEN saved.snapshot_id ELSE next.snapshot_id END,
		workloads=CASE WHEN saved.workloads=$5::jsonb THEN saved.workloads ELSE $5::jsonb END,watch_binding=$6,watch_versions=$7::jsonb
	FROM next
	WHERE (saved.scope_id,saved.external_id,saved.credential_revision)=($1,$2,$3)
	  AND (saved.snapshot_id,saved.refresh_started_at)=($8::uuid,$9::timestamptz)
	  AND saved.refresh_started_at<$4`

const insertWorkloadWatch = `WITH next AS (SELECT gen_random_uuid() AS snapshot_id)
	INSERT INTO cmp_cluster_workloads AS saved
		(scope_id,external_id,credential_revision,refresh_started_at,observed_at,workloads,
		 watch_binding,watch_versions,snapshot_id,watch_snapshot_id)
	SELECT $1,$2,$3,$4,clock_timestamp(),$5::jsonb,$6,$7::jsonb,next.snapshot_id,next.snapshot_id FROM next
	ON CONFLICT (scope_id,external_id) DO UPDATE SET
		credential_revision=EXCLUDED.credential_revision,refresh_started_at=EXCLUDED.refresh_started_at,
		observed_at=EXCLUDED.observed_at,workloads=EXCLUDED.workloads,snapshot_id=EXCLUDED.snapshot_id,
		watch_binding=EXCLUDED.watch_binding,watch_versions=EXCLUDED.watch_versions,watch_snapshot_id=EXCLUDED.watch_snapshot_id
	WHERE saved.credential_revision<EXCLUDED.credential_revision`
