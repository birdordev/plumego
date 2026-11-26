package publiccloud

import (
	"context"
	"slices"

	"with-cloud-manager/internal/provider"
)

func cacheObject(id, name, status, product, engine, version, network string, capacityMiB float64) (provider.Object, error) {
	item := provider.Object{
		ID: id, Name: name, Status: status,
		Cache: &provider.CacheDetails{Product: product, Engine: engine, Version: version, CapacityMiB: capacityMiB},
	}
	if network != "0" {
		item.Link(provider.Network, network, "in_network")
	}
	if !resourceID.MatchString(id) || (network != "" && !resourceID.MatchString(network)) || !item.ValidCache() {
		return provider.Object{}, &provider.Failure{Kind: provider.InvalidResponse}
	}
	return item, nil
}

func (c *Client) cacheReader() regionalInstanceReader {
	switch c.name {
	case provider.TencentCVM:
		return c.tencentCaches
	case provider.AlibabaECS:
		return c.alibabaCaches
	case provider.HuaweiECS:
		return c.huaweiCaches
	default:
		return nil
	}
}

func (c *Client) discoverCaches(ctx context.Context, region, cursor string) (provider.Page, error) {
	if c.name == provider.HuaweiECS {
		return c.discoverHuaweiCaches(ctx, region, cursor)
	}
	reader := c.cacheReader()
	if reader == nil {
		return provider.Page{}, &provider.Failure{Kind: provider.Unsupported}
	}
	return discoverRegionalInstances(ctx, region, cursor, regionalInstancePageSize, []regionalInstanceReader{reader})
}

func (c *Client) observeCache(ctx context.Context, scope provider.Scope, id string) (provider.Object, error) {
	if scope.Provider != c.name || scope.RemoteIdentity != c.identity {
		return provider.Object{}, &provider.Failure{Kind: provider.Unauthorized}
	}
	reader := c.cacheReader()
	if reader == nil || !slices.Contains(c.profile.Regions, scope.Region) || !cacheID(c.name, id) {
		return provider.Object{}, &provider.Failure{Kind: provider.Unsupported}
	}
	item, err := observeRegionalInstance(ctx, scope.Region, id, reader)
	if provider.IsFailure(err, provider.Absent) && !c.profile.CacheListScopeWide {
		return provider.Object{}, &provider.Failure{Kind: provider.OutcomeUnknown}
	}
	return item, err
}

func cacheID(name, id string) bool {
	if name == provider.HuaweiECS {
		return huaweiResourceID.MatchString(id)
	}
	return resourceID.MatchString(id)
}
