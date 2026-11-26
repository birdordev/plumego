package publiccloud

import (
	"context"
	"net/http"

	"with-cloud-manager/internal/provider"
)

type alibabaPoolRow struct {
	Info struct {
		ID         string `json:"nodepool_id"`
		Region     string `json:"region_id"`
		Cluster    string `json:"cluster_id"`
		Name, Type string
	} `json:"nodepool_info"`
	Status struct {
		State string
		Total *int `json:"total_nodes"`
	}
	Autoscaling struct {
		Enable *bool
		Min    *int `json:"min_instances"`
		Max    *int `json:"max_instances"`
	} `json:"auto_scaling"`
	ScalingGroup struct {
		Desired *int `json:"desired_size"`
	} `json:"scaling_group"`
}

func (c *Client) alibabaClusterNodePools(ctx context.Context, region, cluster string) ([]provider.ClusterNodePool, error) {
	var result struct {
		Code  string
		Pools []alibabaPoolRow `json:"nodepools"`
	}
	if err := c.alibabaSigned(ctx, http.MethodGet, "cs."+region+".aliyuncs.com",
		"/clusters/"+cluster+"/nodepools", "DescribeClusterNodePools", "2015-12-15", nil, "", &result); err != nil {
		return nil, err
	}
	if result.Code != "" {
		return nil, cloudFailure(result.Code)
	}
	if result.Pools == nil || len(result.Pools) > provider.MaxClusterNodePools {
		return nil, &provider.Failure{Kind: provider.InvalidResponse}
	}
	pools := make([]provider.ClusterNodePool, 0, len(result.Pools))
	for _, row := range result.Pools {
		if (row.Info.Region != "" && row.Info.Region != region) || (row.Info.Cluster != "" && row.Info.Cluster != cluster) {
			return nil, &provider.Failure{Kind: provider.InvalidResponse}
		}
		pools = append(pools, provider.ClusterNodePool{
			ID: row.Info.ID, Name: row.Info.Name, Type: row.Info.Type, Status: row.Status.State,
			CurrentNodes: row.Status.Total, DesiredNodes: row.ScalingGroup.Desired,
			MinNodes: row.Autoscaling.Min, MaxNodes: row.Autoscaling.Max, Autoscaling: row.Autoscaling.Enable,
		})
	}
	return pools, nil
}
