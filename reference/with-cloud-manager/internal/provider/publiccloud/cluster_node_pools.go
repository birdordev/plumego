package publiccloud

import (
	"context"
	"slices"
	"strings"

	"with-cloud-manager/internal/provider"
)

func (c *Client) ReadClusterNodePools(ctx context.Context, scope provider.Scope, id string) ([]provider.ClusterNodePool, error) {
	if scope.Provider != c.name || scope.RemoteIdentity != c.identity {
		return nil, &provider.Failure{Kind: provider.Unauthorized}
	}
	if scope.Kind != provider.KubernetesCluster || !slices.Contains(c.profile.Regions, scope.Region) || !resourceID.MatchString(id) {
		return nil, &provider.Failure{Kind: provider.Unsupported}
	}
	switch c.name {
	case provider.TencentCVM:
		if strings.HasPrefix(id, "cls-") && len(id) > len("cls-") {
			return c.tencentClusterNodePools(ctx, scope.Region, id)
		}
	case provider.AlibabaECS:
		return c.alibabaClusterNodePools(ctx, scope.Region, id)
	case provider.HuaweiECS:
		if huaweiResourceID.MatchString(id) {
			return c.huaweiClusterNodePools(ctx, scope.Region, id)
		}
	}
	return nil, &provider.Failure{Kind: provider.Unsupported}
}
