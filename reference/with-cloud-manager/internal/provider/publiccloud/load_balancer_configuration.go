package publiccloud

import (
	"context"
	"slices"
	"strings"

	"with-cloud-manager/internal/provider"
)

const maxLoadBalancerConfigurationPages = 20

func (c *Client) ReadLoadBalancerConfiguration(ctx context.Context, scope provider.Scope, request provider.LoadBalancerConfigurationRequest) (provider.LoadBalancerConfiguration, error) {
	if scope.Kind != provider.LoadBalancer {
		return provider.LoadBalancerConfiguration{}, &provider.Failure{Kind: provider.Unsupported}
	}
	if scope.Provider != c.name || scope.RemoteIdentity != c.identity {
		return provider.LoadBalancerConfiguration{}, &provider.Failure{Kind: provider.Unauthorized}
	}
	if !slices.Contains(c.profile.Regions, scope.Region) {
		return provider.LoadBalancerConfiguration{}, &provider.Failure{Kind: provider.Unsupported}
	}
	var cfg provider.LoadBalancerConfiguration
	var err error
	id := request.ID
	var backends *loadBalancerBackendRead
	if request.IncludeBackends {
		if len(request.BackendSecret) != 0 && len(request.BackendSecret) != 32 {
			return cfg, invalidLoadBalancerBackend()
		}
		backends = newLoadBalancerBackendRead(request.BackendSecret)
	}
	if c.name == provider.TencentCVM {
		cfg, err = c.tencentLoadBalancerConfiguration(ctx, scope.Region, id, backends)
	} else {
		var parent provider.Object
		parent, err = c.observeLoadBalancer(ctx, scope, id)
		if err != nil {
			return cfg, err
		}
		switch c.name {
		case provider.AlibabaECS:
			cfg, err = c.alibabaLoadBalancerConfiguration(ctx, scope.Region, id, parent.LoadBalancer.Product, backends)
		case provider.HuaweiECS:
			cfg, err = c.huaweiLoadBalancerConfiguration(ctx, scope.Region, id, backends)
		default:
			err = &provider.Failure{Kind: provider.Unsupported}
		}
	}
	if err != nil {
		return provider.LoadBalancerConfiguration{}, err
	}
	if backends != nil {
		cfg.Backends = &backends.snapshot
	}
	if err := cfg.Normalize(); err != nil {
		return provider.LoadBalancerConfiguration{}, err
	}
	return cfg, nil
}

func readLoadBalancerPages[T any](ctx context.Context, fetch func(context.Context, string) ([]T, string, error)) ([]T, error) {
	items := make([]T, 0)
	seen := make(map[string]struct{}, maxLoadBalancerConfigurationPages)
	cursor := ""
	for range maxLoadBalancerConfigurationPages {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		rows, next, err := fetch(ctx, cursor)
		if err != nil {
			return nil, err
		}
		if rows == nil || len(rows) > loadBalancerPageSize ||
			len(items)+len(rows) > provider.MaxLoadBalancerComponents ||
			len(next) > provider.MaxCursorBytes || strings.ContainsRune(next, 0) {
			return nil, &provider.Failure{Kind: provider.InvalidResponse}
		}
		items = append(items, rows...)
		if next == "" {
			return items, nil
		}
		if _, duplicate := seen[next]; duplicate || len(rows) == 0 {
			return nil, &provider.Failure{Kind: provider.InvalidResponse}
		}
		seen[next] = struct{}{}
		cursor = next
	}
	return nil, &provider.Failure{Kind: provider.InvalidResponse}
}

func configurationGroupIDs(listeners []provider.LoadBalancerListener) []string {
	unique := make(map[string]struct{})
	for _, listener := range listeners {
		for _, id := range listener.TargetGroupIDs {
			unique[id] = struct{}{}
		}
	}
	ids := make([]string, 0, len(unique))
	for id := range unique {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	return ids
}
