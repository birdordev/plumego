package tenant

import (
	"context"
	"sync"
	"time"
)

// RoutePolicyCache caches route policies by tenant id.
type RoutePolicyCache interface {
	Get(ctx context.Context, tenantID string) (RoutePolicy, bool)
	Set(ctx context.Context, tenantID string, policy RoutePolicy) error
	Delete(ctx context.Context, tenantID string) error
}

// InMemoryRoutePolicyCache is a TTL cache for route policies.
type InMemoryRoutePolicyCache struct {
	mu      sync.RWMutex
	entries map[string]routePolicyCacheEntry
	maxSize int
	ttl     time.Duration
}

type routePolicyCacheEntry struct {
	policy    RoutePolicy
	expiresAt time.Time
}

// NewInMemoryRoutePolicyCache creates a cache with size and ttl limits.
func NewInMemoryRoutePolicyCache(maxSize int, ttl time.Duration) *InMemoryRoutePolicyCache {
	if maxSize <= 0 {
		maxSize = 1000
	}
	if ttl <= 0 {
		ttl = 5 * time.Minute
	}

	return &InMemoryRoutePolicyCache{
		entries: make(map[string]routePolicyCacheEntry),
		maxSize: maxSize,
		ttl:     ttl,
	}
}

// Get returns a cached policy if present and not expired.
// Expired entries remain eligible for eviction when the cache needs space.
func (c *InMemoryRoutePolicyCache) Get(_ context.Context, tenantID string) (RoutePolicy, bool) {
	if c == nil {
		return RoutePolicy{}, false
	}
	c.mu.RLock()
	entry, ok := c.entries[tenantID]
	c.mu.RUnlock()

	if !ok {
		return RoutePolicy{}, false
	}
	if time.Now().UTC().After(entry.expiresAt) {
		return RoutePolicy{}, false
	}
	return entry.policy, true
}

// Set stores a policy in the cache.
func (c *InMemoryRoutePolicyCache) Set(ctx context.Context, tenantID string, policy RoutePolicy) error {
	if c == nil {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()

	if len(c.entries) >= c.maxSize {
		now := time.Now().UTC()
		evicted := false
		for key, entry := range c.entries {
			if now.After(entry.expiresAt) {
				delete(c.entries, key)
				evicted = true
				break
			}
		}
		if !evicted {
			// No expired entry found; evict an arbitrary entry to stay within maxSize.
			for key := range c.entries {
				delete(c.entries, key)
				break
			}
		}
	}

	c.entries[tenantID] = routePolicyCacheEntry{
		policy:    policy,
		expiresAt: time.Now().UTC().Add(c.ttl),
	}
	return nil
}

// Delete removes a cached policy.
func (c *InMemoryRoutePolicyCache) Delete(ctx context.Context, tenantID string) error {
	if c == nil {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.entries, tenantID)
	return nil
}
