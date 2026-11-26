package publiccloud

import (
	"context"
	"net/url"

	"with-cloud-manager/internal/provider"
)

type huaweiPoolRow struct {
	Metadata struct{ UID, Name, ClusterID, ProjectID string }
	Spec     struct {
		Type             string
		InitialNodeCount *int
		Autoscaling      struct {
			Enable                     *bool
			MinNodeCount, MaxNodeCount *int
		}
	}
	Status struct {
		Phase       string
		CurrentNode *int
	}
}

func (c *Client) huaweiClusterNodePools(ctx context.Context, region, cluster string) ([]provider.ClusterNodePool, error) {
	endpoint := url.URL{
		Scheme: "https", Host: "cce." + region + ".myhuaweicloud.com",
		Path:     "/api/v3/projects/" + c.profile.ProjectID + "/clusters/" + cluster + "/nodepools",
		RawQuery: "showDefaultNodePool=true",
	}
	var result struct {
		Kind, APIVersion string
		Items            []huaweiPoolRow
	}
	if err := c.readHuawei(ctx, endpoint, &result); err != nil {
		return nil, err
	}
	if result.Kind != "List" || result.APIVersion != "v3" || result.Items == nil || len(result.Items) > provider.MaxClusterNodePools {
		return nil, &provider.Failure{Kind: provider.InvalidResponse}
	}
	pools := make([]provider.ClusterNodePool, 0, len(result.Items))
	for _, row := range result.Items {
		if (row.Metadata.ProjectID != "" && row.Metadata.ProjectID != c.profile.ProjectID) ||
			(row.Metadata.ClusterID != "" && row.Metadata.ClusterID != cluster) {
			return nil, &provider.Failure{Kind: provider.Unauthorized}
		}
		pools = append(pools, provider.ClusterNodePool{
			ID: row.Metadata.UID, Name: row.Metadata.Name, Type: row.Spec.Type, Status: row.Status.Phase,
			CurrentNodes: row.Status.CurrentNode, DesiredNodes: row.Spec.InitialNodeCount,
			MinNodes: row.Spec.Autoscaling.MinNodeCount, MaxNodes: row.Spec.Autoscaling.MaxNodeCount, Autoscaling: row.Spec.Autoscaling.Enable,
		})
	}
	return pools, nil
}
