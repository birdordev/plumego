package tenant

import (
	"context"
	"maps"
	"slices"
	"sync"
	"time"
)

// RoutePolicy describes tenant-specific routing policy.
// Payload is application-defined JSON to keep the framework unopinionated.
type RoutePolicy struct {
	TenantID  string
	Strategy  string
	Payload   []byte
	Metadata  map[string]string
	UpdatedAt time.Time
}

func cloneRoutePolicy(policy RoutePolicy) RoutePolicy {
	policy.Payload = slices.Clone(policy.Payload)
	policy.Metadata = maps.Clone(policy.Metadata)
	return policy
}

// RoutePolicyProvider loads routing policy for a tenant.
type RoutePolicyProvider interface {
	RoutePolicy(ctx context.Context, tenantID string) (RoutePolicy, error)
}

// RoutePolicyStore persists routing policy for a tenant.
type RoutePolicyStore interface {
	RoutePolicyProvider
	SetRoutePolicy(ctx context.Context, policy RoutePolicy) error
	DeleteRoutePolicy(ctx context.Context, tenantID string) error
}

// InMemoryRoutePolicyStore stores route policies in memory. The store copies
// Payload and Metadata on both reads and writes.
type InMemoryRoutePolicyStore struct {
	mu      sync.RWMutex
	entries map[string]RoutePolicy
}

// NewInMemoryRoutePolicyStore creates an in-memory route policy store.
func NewInMemoryRoutePolicyStore() *InMemoryRoutePolicyStore {
	return &InMemoryRoutePolicyStore{
		entries: make(map[string]RoutePolicy),
	}
}

// RoutePolicy returns the current policy for a tenant.
func (s *InMemoryRoutePolicyStore) RoutePolicy(ctx context.Context, tenantID string) (RoutePolicy, error) {
	if s == nil {
		return RoutePolicy{}, ErrRoutePolicyNotFound
	}
	s.mu.RLock()
	policy, ok := s.entries[tenantID]
	s.mu.RUnlock()
	if !ok {
		return RoutePolicy{}, ErrRoutePolicyNotFound
	}
	return cloneRoutePolicy(policy), nil
}

// SetRoutePolicy stores policy for a tenant.
func (s *InMemoryRoutePolicyStore) SetRoutePolicy(ctx context.Context, policy RoutePolicy) error {
	if s == nil {
		return nil
	}
	if policy.UpdatedAt.IsZero() {
		policy.UpdatedAt = time.Now().UTC()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.entries[policy.TenantID] = cloneRoutePolicy(policy)
	return nil
}

// DeleteRoutePolicy removes policy for a tenant.
func (s *InMemoryRoutePolicyStore) DeleteRoutePolicy(ctx context.Context, tenantID string) error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.entries, tenantID)
	return nil
}
