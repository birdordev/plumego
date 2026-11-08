package cache

import (
	"context"
	"fmt"
	"strings"
	"time"

	storecache "github.com/birdordev/plumego/store/cache"
	"github.com/birdordev/plumego/x/tenant/core"
)

// TenantCache wraps a Cache implementation and automatically scopes keys by tenant ID.
// This prevents cross-tenant cache pollution and ensures data isolation.
type TenantCache struct {
	cache     storecache.Cache
	keyPrefix string
	separator string
}

// NewTenantCache creates a new tenant-scoped cache wrapper.
// Keys will be automatically prefixed with the tenant ID from context.
func NewTenantCache(cache storecache.Cache, options ...TenantCacheOption) *TenantCache {
	tc := &TenantCache{
		cache:     cache,
		keyPrefix: "tenant",
		separator: ":",
	}

	for _, opt := range options {
		opt(tc)
	}

	return tc
}

// TenantCacheOption configures a TenantCache.
type TenantCacheOption func(*TenantCache)

// WithKeyPrefix sets a custom prefix for all tenant cache keys.
func WithKeyPrefix(prefix string) TenantCacheOption {
	return func(tc *TenantCache) {
		tc.keyPrefix = prefix
	}
}

// WithSeparator sets a custom separator between prefix, tenant ID, and key.
func WithSeparator(sep string) TenantCacheOption {
	return func(tc *TenantCache) {
		tc.separator = sep
	}
}

func (tc *TenantCache) Get(ctx context.Context, key string) ([]byte, error) {
	scopedKey, err := tc.scopeKey(ctx, key)
	if err != nil {
		return nil, err
	}
	return tc.cache.Get(ctx, scopedKey)
}

func (tc *TenantCache) Set(ctx context.Context, key string, value []byte, ttl time.Duration) error {
	scopedKey, err := tc.scopeKey(ctx, key)
	if err != nil {
		return err
	}
	return tc.cache.Set(ctx, scopedKey, value, ttl)
}

func (tc *TenantCache) Delete(ctx context.Context, key string) error {
	scopedKey, err := tc.scopeKey(ctx, key)
	if err != nil {
		return err
	}
	return tc.cache.Delete(ctx, scopedKey)
}

func (tc *TenantCache) Exists(ctx context.Context, key string) (bool, error) {
	scopedKey, err := tc.scopeKey(ctx, key)
	if err != nil {
		return false, err
	}
	return tc.cache.Exists(ctx, scopedKey)
}

func (tc *TenantCache) Clear(ctx context.Context) error {
	tenantID := tenant.TenantIDFromContext(ctx)
	if tenantID == "" {
		return tenant.ErrTenantNotFound
	}
	if err := tc.validateTenantID(tenantID); err != nil {
		return err
	}
	return storecache.ErrCapabilityUnsupported
}

func (tc *TenantCache) Incr(ctx context.Context, key string, delta int64) (int64, error) {
	scopedKey, err := tc.scopeKey(ctx, key)
	if err != nil {
		return 0, err
	}
	counter, ok := tc.cache.(storecache.CounterCache)
	if !ok {
		return 0, storecache.ErrCapabilityUnsupported
	}
	return counter.Incr(ctx, scopedKey, delta)
}

func (tc *TenantCache) Decr(ctx context.Context, key string, delta int64) (int64, error) {
	scopedKey, err := tc.scopeKey(ctx, key)
	if err != nil {
		return 0, err
	}
	counter, ok := tc.cache.(storecache.CounterCache)
	if !ok {
		return 0, storecache.ErrCapabilityUnsupported
	}
	return counter.Decr(ctx, scopedKey, delta)
}

func (tc *TenantCache) Append(ctx context.Context, key string, data []byte) error {
	scopedKey, err := tc.scopeKey(ctx, key)
	if err != nil {
		return err
	}
	appender, ok := tc.cache.(storecache.AppenderCache)
	if !ok {
		return storecache.ErrCapabilityUnsupported
	}
	return appender.Append(ctx, scopedKey, data)
}

// RawCache returns the underlying cache for operations that need direct access.
// Use with caution as this bypasses tenant isolation.
func (tc *TenantCache) RawCache() storecache.Cache {
	return tc.cache
}

func (tc *TenantCache) scopeKey(ctx context.Context, key string) (string, error) {
	tenantID := tenant.TenantIDFromContext(ctx)
	if tenantID == "" {
		return "", tenant.ErrTenantNotFound
	}

	if err := tc.validateTenantID(tenantID); err != nil {
		return "", err
	}

	return tc.buildKey(tenantID, key), nil
}

func (tc *TenantCache) validateTenantID(tenantID string) error {
	if tenantID == "" {
		return tenant.ErrTenantNotFound
	}

	if strings.Contains(tenantID, tc.separator) {
		return fmt.Errorf("tenant ID cannot contain separator character '%s'", tc.separator)
	}

	for i := 0; i < len(tenantID); i++ {
		c := tenantID[i]
		if c < 0x20 || c == 0x7F {
			return fmt.Errorf("tenant ID contains invalid control character at position %d", i)
		}
	}

	return nil
}

func (tc *TenantCache) buildKey(tenantID, key string) string {
	if tc.keyPrefix == "" {
		return fmt.Sprintf("%s%s%s", tenantID, tc.separator, key)
	}
	return fmt.Sprintf("%s%s%s%s%s", tc.keyPrefix, tc.separator, tenantID, tc.separator, key)
}
