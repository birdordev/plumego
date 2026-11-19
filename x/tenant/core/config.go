// Package tenant provides multi-tenancy infrastructure (EXPERIMENTAL).
//
// ⚠️  EXPERIMENTAL: This package's API may change in minor versions.
//
// The tenant package enables multi-tenancy support with per-tenant
// configuration, quota enforcement, and policy management. However,
// it is not yet feature-complete for production use.
//
// Current limitations:
//   - Experimental API surface (may change in minor versions)
//   - Limited integration tests across storage backends
//   - No dedicated tenant guide beyond the main README
//
// For production multi-tenancy, we recommend:
//   - Wiring tenant middleware explicitly (see x/tenant/resolve, x/tenant/ratelimit, x/tenant/quota, x/tenant/policy)
//   - Validating isolation and quota behavior in your environment
//   - Treating the API as experimental until stabilized
//
// Planned for future versions:
//   - Complete middleware integration
//   - Production-ready test suite
//   - Example multi-tenant applications
//   - API stability guarantees
package tenant

import (
	"context"
	"maps"
	"slices"
	"sync"
	"time"
)

// Config defines tenant-level policies, quotas, and rate limits.
type Config struct {
	TenantID  string
	Quota     QuotaConfig
	Policy    PolicyConfig
	RateLimit RateLimitConfig
	Metadata  map[string]string
	UpdatedAt time.Time
}

func (c Config) clone() Config {
	c.Metadata = maps.Clone(c.Metadata)
	c.Quota.Limits = slices.Clone(c.Quota.Limits)
	c.Policy.AllowedModels = slices.Clone(c.Policy.AllowedModels)
	c.Policy.AllowedTools = slices.Clone(c.Policy.AllowedTools)
	c.Policy.AllowedMethods = slices.Clone(c.Policy.AllowedMethods)
	c.Policy.AllowedPaths = slices.Clone(c.Policy.AllowedPaths)
	return c
}

// QuotaConfigProvider loads quota configuration for a tenant.
type QuotaConfigProvider interface {
	QuotaConfig(ctx context.Context, tenantID string) (QuotaConfig, error)
}

// PolicyConfigProvider loads policy configuration for a tenant.
type PolicyConfigProvider interface {
	PolicyConfig(ctx context.Context, tenantID string) (PolicyConfig, error)
}

// ConfigManager is an app-layer facade convenience interface that composes all
// tenant configuration sub-interfaces into one. It is intended for use at
// application wiring points only — subsystems should depend on their specific
// provider sub-interface (QuotaConfigProvider, PolicyConfigProvider, or
// RateLimitConfigProvider) rather than on ConfigManager directly. This keeps
// module coupling minimal; ConfigManager is for app wiring only.
//
// It also satisfies QuotaConfigProvider, PolicyConfigProvider, and
// RateLimitConfigProvider so a single implementation can be passed
// directly to quota, policy, and rate limit subsystems.
type ConfigManager interface {
	QuotaConfigProvider
	PolicyConfigProvider
	RateLimitConfigProvider
	GetTenantConfig(ctx context.Context, tenantID string) (Config, error)
}

// InMemoryConfigManager stores tenant configs in memory.
type InMemoryConfigManager struct {
	mu      sync.RWMutex
	configs map[string]Config
}

// NewInMemoryConfigManager creates an in-memory config manager.
func NewInMemoryConfigManager() *InMemoryConfigManager {
	return &InMemoryConfigManager{
		configs: make(map[string]Config),
	}
}

// SetTenantConfig inserts or replaces a tenant configuration with a snapshot.
func (m *InMemoryConfigManager) SetTenantConfig(cfg Config) {
	if m == nil {
		return
	}

	cfg = cfg.clone()
	m.mu.Lock()
	defer m.mu.Unlock()

	cfg.UpdatedAt = time.Now().UTC()
	m.configs[cfg.TenantID] = cfg
}

// GetTenantConfig returns a caller-owned copy of the tenant configuration.
func (m *InMemoryConfigManager) GetTenantConfig(ctx context.Context, tenantID string) (Config, error) {
	if m == nil {
		return Config{}, ErrTenantNotFound
	}

	m.mu.RLock()
	cfg, ok := m.configs[tenantID]
	m.mu.RUnlock()
	if !ok {
		return Config{}, ErrTenantNotFound
	}
	return cfg.clone(), nil
}

// QuotaConfig returns the quota config for a tenant.
func (m *InMemoryConfigManager) QuotaConfig(ctx context.Context, tenantID string) (QuotaConfig, error) {
	cfg, err := m.GetTenantConfig(ctx, tenantID)
	if err != nil {
		return QuotaConfig{}, err
	}
	return cfg.Quota, nil
}

// PolicyConfig returns the policy config for a tenant.
func (m *InMemoryConfigManager) PolicyConfig(ctx context.Context, tenantID string) (PolicyConfig, error) {
	cfg, err := m.GetTenantConfig(ctx, tenantID)
	if err != nil {
		return PolicyConfig{}, err
	}
	return cfg.Policy, nil
}

// RateLimitConfig returns the rate limit config for a tenant.
// This satisfies RateLimitConfigProvider so InMemoryConfigManager can be passed
// directly to NewTokenBucketRateLimiter, removing the need for a separate
// InMemoryRateLimitProvider when using the unified Config store.
func (m *InMemoryConfigManager) RateLimitConfig(ctx context.Context, tenantID string) (RateLimitConfig, error) {
	cfg, err := m.GetTenantConfig(ctx, tenantID)
	if err != nil {
		return RateLimitConfig{}, err
	}
	return cfg.RateLimit, nil
}
