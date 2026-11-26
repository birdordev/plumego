package publiccloud

import (
	"context"
	"encoding/json"
	"strings"

	"with-cloud-manager/internal/provider"
)

type tencentClusterRow struct {
	ClusterId, ClusterName, ClusterStatus, ClusterType, ClusterVersion string
	ClusterNodeNum                                                     *int
	ClusterNetworkSettings                                             struct{ VpcId string }
}

func (c *Client) tencentClusters(ctx context.Context, region string, offset int, id string) ([]provider.Object, *int, error) {
	input := struct {
		Offset, Limit int
		ClusterIds    []string `json:",omitempty"`
	}{Offset: offset, Limit: regionalInstancePageSize}
	if id != "" {
		input.ClusterIds = []string{id}
	}
	body, err := json.Marshal(input)
	if err != nil {
		return nil, nil, err
	}
	var result struct {
		Clusters   []tencentClusterRow
		TotalCount *int
	}
	if err := c.tencent(ctx, "tke", region, "DescribeClusters", "2018-05-25", body, &result); err != nil {
		return nil, nil, err
	}
	return projectRegionalInstances(result.Clusters, result.TotalCount, func(row tencentClusterRow) (provider.Object, error) {
		if !strings.HasPrefix(row.ClusterId, "cls-") || len(row.ClusterId) <= len("cls-") {
			return provider.Object{}, &provider.Failure{Kind: provider.InvalidResponse}
		}
		return clusterObject(row.ClusterId, row.ClusterName, row.ClusterStatus, row.ClusterNetworkSettings.VpcId,
			provider.ClusterDetails{Product: provider.TencentTKE, Type: row.ClusterType, Version: row.ClusterVersion, NodeCount: row.ClusterNodeNum})
	})
}
