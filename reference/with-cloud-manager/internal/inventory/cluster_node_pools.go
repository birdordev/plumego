package inventory

import (
	"context"
	"database/sql"
	"errors"

	dbutil "github.com/birdordev/plumego/store/db"

	"with-cloud-manager/internal/provider"
)

var (
	ErrClusterNodePoolsNotFound = errors.New("cluster node pools unavailable")
	ErrClusterNodePoolsChanged  = errors.New("cluster node pool snapshot changed")
)

var nodePoolSnapshots = clusterSnapshotSource{
	table: "cmp_cluster_node_pools", column: "pools",
	maxRows: provider.MaxClusterNodePools, maxBytes: 1 << 20,
	notFound: ErrClusterNodePoolsNotFound, changed: ErrClusterNodePoolsChanged,
}

type ClusterNodePoolsPage struct {
	ClusterSnapshotInfo
	Pools []provider.ClusterNodePool `json:"node_pools"`
}

func (s Store) RefreshClusterNodePools(ctx context.Context, key Key, id string, revision int64, adapter provider.ClusterNodePoolReader) error {
	publish, err := s.PrepareClusterNodePools(ctx, key, id, revision, adapter)
	if err != nil {
		return err
	}
	return dbutil.WithTransaction(ctx, s.db, nil, func(tx *sql.Tx) error { return publish(ctx, tx) })
}

func (s Store) PrepareClusterNodePools(ctx context.Context, key Key, id string, revision int64, adapter provider.ClusterNodePoolReader) (func(context.Context, *sql.Tx) error, error) {
	return prepareClusterSnapshot(ctx, s, key, id, revision, nodePoolSnapshots, adapter.ReadClusterNodePools, provider.NormalizeClusterNodePools)
}

func (s Store) ReadClusterNodePools(ctx context.Context, token string, q ClusterSnapshotQuery) (ClusterNodePoolsPage, error) {
	info, items, err := readClusterSnapshot[provider.ClusterNodePool](ctx, s, token, q, nodePoolSnapshots)
	return ClusterNodePoolsPage{ClusterSnapshotInfo: info, Pools: items}, err
}
