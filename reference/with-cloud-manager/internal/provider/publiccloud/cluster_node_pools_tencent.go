package publiccloud

import (
	"context"
	"encoding/json"

	"with-cloud-manager/internal/provider"
)

type tencentPoolRow struct {
	ClusterId, NodePoolId, Name, LifeState, Type string
	Regular                                      *struct {
		DesiredNodesNum, MinNodesNum, MaxNodesNum *int
		AutoscalingGroupStatus                    string
		NodeCountSummary                          *struct {
			AutoscalingAdded, ManuallyAdded struct{ Total *int }
		}
	}
	Native *struct {
		EnableAutoscaling *bool
		Replicas          *int
		Scaling           struct{ MinReplicas, MaxReplicas *int }
	}
	External *struct{ NodesNum *int }
}

func (c *Client) tencentClusterNodePools(ctx context.Context, region, cluster string) ([]provider.ClusterNodePool, error) {
	const limit = 50
	total := -1
	pools := make([]provider.ClusterNodePool, 0, limit)
	for offset := 0; total < 0 || offset < total; offset += limit {
		body, err := json.Marshal(struct {
			ClusterId     string
			Offset, Limit int
		}{ClusterId: cluster, Offset: offset, Limit: limit})
		if err != nil {
			return nil, err
		}
		var page struct {
			NodePools  []tencentPoolRow
			TotalCount *int
		}
		if err := c.tencent(ctx, "tke", region, "DescribeNodePools", "2022-05-01", body, &page); err != nil {
			return nil, err
		}
		if page.TotalCount == nil || *page.TotalCount < 0 || *page.TotalCount > provider.MaxClusterNodePools || page.NodePools == nil {
			return nil, &provider.Failure{Kind: provider.InvalidResponse}
		}
		if total < 0 {
			total = *page.TotalCount
		}
		if *page.TotalCount != total || len(page.NodePools) != min(limit, total-offset) {
			return nil, &provider.Failure{Kind: provider.ExpiredCursor}
		}
		for _, row := range page.NodePools {
			if row.ClusterId != cluster {
				return nil, &provider.Failure{Kind: provider.InvalidResponse}
			}
			pool, err := row.project()
			if err != nil {
				return nil, err
			}
			pools = append(pools, pool)
		}
	}
	return pools, nil
}

func (row tencentPoolRow) project() (provider.ClusterNodePool, error) {
	pool := provider.ClusterNodePool{ID: row.NodePoolId, Name: row.Name, Type: row.Type, Status: row.LifeState}
	if row.Type == "External" && row.External != nil {
		pool.CurrentNodes = row.External.NodesNum
	}
	if row.Native != nil {
		pool.DesiredNodes, pool.Autoscaling = row.Native.Replicas, row.Native.EnableAutoscaling
		pool.MinNodes, pool.MaxNodes = row.Native.Scaling.MinReplicas, row.Native.Scaling.MaxReplicas
	}
	if row.Type != "Regular" || row.Regular == nil {
		return pool, nil
	}
	regular := row.Regular
	pool.DesiredNodes, pool.MinNodes, pool.MaxNodes = regular.DesiredNodesNum, regular.MinNodesNum, regular.MaxNodesNum
	switch regular.AutoscalingGroupStatus {
	case "enabled", "disabled":
		enabled := regular.AutoscalingGroupStatus == "enabled"
		pool.Autoscaling = &enabled
	}
	if counts := regular.NodeCountSummary; counts != nil {
		a, m := counts.AutoscalingAdded.Total, counts.ManuallyAdded.Total
		if a != nil && m != nil {
			if *a < 0 || *m < 0 || *a > 1_000_000 || *m > 1_000_000 {
				return provider.ClusterNodePool{}, &provider.Failure{Kind: provider.InvalidResponse}
			}
			sum := *a + *m
			pool.CurrentNodes = &sum
		}
	}
	return pool, nil
}
