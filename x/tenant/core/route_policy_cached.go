package tenant

import "context"

// CachedRoutePolicyProvider wraps a provider with a cache.
type CachedRoutePolicyProvider struct {
	provider RoutePolicyProvider
	cache    RoutePolicyCache
}

// NewCachedRoutePolicyProvider creates a cached provider.
func NewCachedRoutePolicyProvider(provider RoutePolicyProvider, cache RoutePolicyCache) *CachedRoutePolicyProvider {
	return &CachedRoutePolicyProvider{
		provider: provider,
		cache:    cache,
	}
}

// RoutePolicy checks the cache before querying the provider. It returns
// the resolved policy even if populating the cache fails.
func (c *CachedRoutePolicyProvider) RoutePolicy(ctx context.Context, tenantID string) (RoutePolicy, error) {
	if c == nil || c.provider == nil {
		return RoutePolicy{}, ErrRoutePolicyNotFound
	}

	if c.cache != nil {
		if policy, ok := c.cache.Get(ctx, tenantID); ok {
			return policy, nil
		}
	}

	policy, err := c.provider.RoutePolicy(ctx, tenantID)
	if err != nil {
		return RoutePolicy{}, err
	}

	if c.cache != nil {
		_ = c.cache.Set(ctx, tenantID, policy)
	}

	return policy, nil
}

// Invalidate removes a cached policy.
func (c *CachedRoutePolicyProvider) Invalidate(ctx context.Context, tenantID string) error {
	if c == nil || c.cache == nil {
		return nil
	}
	return c.cache.Delete(ctx, tenantID)
}
