package inventory

import (
	"context"
	"database/sql"
	"errors"

	"with-cloud-manager/internal/provider"
)

var (
	ErrClusterWorkloadsNotFound = errors.New("cluster workloads unavailable")
	ErrClusterWorkloadsChanged  = errors.New("cluster workload snapshot changed")
)

var workloadSnapshots = clusterSnapshotSource{
	table: "cmp_cluster_workloads", column: "workloads",
	maxRows: provider.MaxClusterWorkloads, maxBytes: 4 << 20,
	notFound: ErrClusterWorkloadsNotFound, changed: ErrClusterWorkloadsChanged,
}

type ClusterWorkloadsPage struct {
	ClusterSnapshotInfo
	Workloads []provider.ClusterWorkload `json:"workloads"`
}

func (s Store) PrepareClusterWorkloads(ctx context.Context, key Key, id string, revision int64,
	read func(context.Context) ([]provider.ClusterWorkload, error),
) (func(context.Context, *sql.Tx) error, error) {
	return prepareClusterSnapshot(ctx, s, key, id, revision, workloadSnapshots,
		func(ctx context.Context, _ provider.Scope, _ string) ([]provider.ClusterWorkload, error) {
			return read(ctx)
		}, provider.NormalizeClusterWorkloads)
}

func (s Store) ReadClusterWorkloads(ctx context.Context, token string, q ClusterSnapshotQuery) (ClusterWorkloadsPage, error) {
	info, items, err := readClusterSnapshot[provider.ClusterWorkload](ctx, s, token, q, workloadSnapshots)
	return ClusterWorkloadsPage{ClusterSnapshotInfo: info, Workloads: items}, err
}
