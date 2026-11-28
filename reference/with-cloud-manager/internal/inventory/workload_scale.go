package inventory

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"with-cloud-manager/internal/identity"
	"with-cloud-manager/internal/provider"
)

func WorkloadScaleTarget(ctx context.Context, tx *sql.Tx, scope MutationTarget, id, snapshot string, target provider.WorkloadScaleTarget) (provider.ClusterWorkload, error) {
	if snapshot != "" && !clusterSnapshotID.MatchString(snapshot) {
		return provider.ClusterWorkload{}, identity.ErrAccountInvalid
	}
	var raw []byte
	err := tx.QueryRowContext(ctx, `SELECT item.value FROM cmp_cluster_workloads AS snapshot
		CROSS JOIN LATERAL jsonb_array_elements(snapshot.workloads) AS item(value)
		WHERE (snapshot.scope_id,snapshot.external_id,snapshot.credential_revision)=($1,$2,$3)
		  AND ($4='' OR snapshot.snapshot_id::text=$4)
		  AND (item.value->>'uid',item.value->>'namespace',item.value->>'kind',item.value->>'name')=($5,$6,$7,$8)
		FOR SHARE OF snapshot`,
		scope.ScopeID, id, scope.CredentialRevision, snapshot, target.UID, target.Namespace, target.Kind, target.Name).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return provider.ClusterWorkload{}, identity.ErrAccountStale
	}
	if err != nil {
		return provider.ClusterWorkload{}, err
	}
	var row provider.ClusterWorkload
	err = json.Unmarshal(raw, &row)
	return row, err
}

func workloadChangeActive(ctx context.Context, tx *sql.Tx, scopeID int64, id string) (bool, error) {
	var active bool
	err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM cmp_workload_change_reservations
		WHERE (scope_id,external_id)=($1,$2))`, scopeID, id).Scan(&active)
	return active, err
}

// RecordWorkloadScale 写入已核实的期望值，并阻止更早启动的采集覆盖结果。
func RecordWorkloadScale(ctx context.Context, tx *sql.Tx, scope MutationTarget, id string, change provider.WorkloadScaleChange) error {
	var raw []byte
	err := tx.QueryRowContext(ctx, `SELECT workloads FROM cmp_cluster_workloads
		WHERE (scope_id,external_id,credential_revision)=($1,$2,$3) FOR UPDATE`,
		scope.ScopeID, id, scope.CredentialRevision).Scan(&raw)
	switch {
	case errors.Is(err, sql.ErrNoRows):
	case err != nil:
		return err
	default:
		if err := updateWorkloadScaleSnapshot(ctx, tx, scope.ScopeID, id, raw, change); err != nil {
			return err
		}
	}
	_, err = tx.ExecContext(ctx, `UPDATE cmp_inventory_objects SET workload_change_generation=workload_change_generation+1
		WHERE (scope_id,external_id)=($1,$2)`, scope.ScopeID, id)
	return err
}

func updateWorkloadScaleSnapshot(ctx context.Context, tx *sql.Tx, scopeID int64, id string, raw []byte, change provider.WorkloadScaleChange) error {
	var items []provider.ClusterWorkload
	if err := json.Unmarshal(raw, &items); err != nil {
		return err
	}
	for i := range items {
		item := &items[i]
		if item.UID != change.UID || item.Namespace != change.Namespace || item.Kind != change.Kind || item.Name != change.Name {
			continue
		}
		if item.Desired != nil && *item.Desired == change.Desired {
			return nil
		}
		if item.Desired == nil || *item.Desired != change.Current {
			return identity.ErrAccountStale
		}
		item.Desired, item.Ready = &change.Desired, nil
		encoded, err := encodeClusterItems(items, workloadSnapshots.maxBytes, provider.NormalizeClusterWorkloads)
		if err != nil {
			return err
		}
		return requireClusterWrite(tx.ExecContext(ctx, `UPDATE cmp_cluster_workloads SET workloads=$3::jsonb,
			snapshot_id=gen_random_uuid(),observed_at=clock_timestamp(),refresh_started_at=clock_timestamp(),
			watch_snapshot_id=NULL,watch_binding=NULL,watch_versions=NULL WHERE (scope_id,external_id)=($1,$2)`,
			scopeID, id, encoded))
	}
	return identity.ErrAccountStale
}
