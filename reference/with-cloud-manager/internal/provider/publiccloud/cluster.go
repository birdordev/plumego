package publiccloud

import (
	"context"
	"slices"

	"with-cloud-manager/internal/provider"
)

func clusterObject(id, name, status, network string, details provider.ClusterDetails) (provider.Object, error) {
	item := provider.Object{ID: id, Name: name, Status: status, Cluster: &details}
	item.Link(provider.Network, network, "in_network")
	if !resourceID.MatchString(id) || (network != "" && !resourceID.MatchString(network)) || !item.ValidCluster() {
		return provider.Object{}, &provider.Failure{Kind: provider.InvalidResponse}
	}
	return item, nil
}

func (c *Client) clusterReader() (regionalInstanceReader, int) {
	switch c.name {
	case provider.TencentCVM:
		return c.tencentClusters, regionalInstancePageSize
	case provider.AlibabaECS:
		return c.alibabaClusters, ackClusterPageSize
	default:
		return nil, 0
	}
}

func (c *Client) discoverClusters(ctx context.Context, region, cursor string) (provider.Page, error) {
	if c.name == provider.HuaweiECS {
		if cursor != "" {
			return provider.Page{}, &provider.Failure{Kind: provider.ExpiredCursor}
		}
		rows, err := c.huaweiClusters(ctx, region)
		return provider.Page{Objects: rows}, err
	}
	read, limit := c.clusterReader()
	if read == nil {
		return provider.Page{}, &provider.Failure{Kind: provider.Unsupported}
	}
	return discoverRegionalInstances(ctx, region, cursor, limit, []regionalInstanceReader{read})
}

func (c *Client) observeCluster(ctx context.Context, scope provider.Scope, id string) (provider.Object, error) {
	if scope.Provider != c.name || scope.RemoteIdentity != c.identity {
		return provider.Object{}, &provider.Failure{Kind: provider.Unauthorized}
	}
	if !slices.Contains(c.profile.Regions, scope.Region) || !resourceID.MatchString(id) {
		return provider.Object{}, &provider.Failure{Kind: provider.Unsupported}
	}
	var item provider.Object
	var err error
	if c.name == provider.HuaweiECS {
		item, err = c.observeHuaweiCluster(ctx, scope, id)
	} else {
		read, _ := c.clusterReader()
		if read == nil {
			return provider.Object{}, &provider.Failure{Kind: provider.Unsupported}
		}
		item, err = observeRegionalInstance(ctx, scope.Region, id, read)
	}
	if provider.IsFailure(err, provider.Absent) && !c.profile.KubernetesListScopeWide {
		return provider.Object{}, &provider.Failure{Kind: provider.OutcomeUnknown}
	}
	return item, err
}

func (c *Client) observeHuaweiCluster(ctx context.Context, scope provider.Scope, id string) (provider.Object, error) {
	if !huaweiResourceID.MatchString(id) {
		return provider.Object{}, &provider.Failure{Kind: provider.Unsupported}
	}
	rows, err := c.huaweiClusters(ctx, scope.Region)
	if err != nil {
		return provider.Object{}, err
	}
	page, err := validateRegionalPage(scope, "", provider.Page{Objects: rows})
	if err != nil {
		return provider.Object{}, err
	}
	for _, item := range page.Objects {
		if item.ID == id {
			return item, nil
		}
	}
	return provider.Object{}, &provider.Failure{Kind: provider.Absent}
}
