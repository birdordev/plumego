package tenant

import (
	"context"
	"errors"
	"sync"
	"time"
)

// RateLimitConfig defines per-tenant rate limiting configuration.
// Zero values mean unlimited.
type RateLimitConfig struct {
	// RequestsPerSecond controls sustained rate.
	RequestsPerSecond int64
	// Burst controls the maximum burst capacity.
	// If zero, defaults to RequestsPerSecond.
	Burst int64
}

// effectiveBurst returns the burst capacity to use for token bucket enforcement.
// If Burst is set, it is used as-is. Otherwise it falls back to RequestsPerSecond.
func (c RateLimitConfig) effectiveBurst() int64 {
	if c.Burst > 0 {
		return c.Burst
	}
	return c.RequestsPerSecond
}

// RateLimitRequest is the input to rate limit checks.
type RateLimitRequest struct {
	// Tokens is the number of tokens to consume (default: 1).
	Tokens int64
	// Now overrides the current time (optional).
	Now time.Time
}

// RateLimitResult describes a rate limit decision.
type RateLimitResult struct {
	Allowed    bool
	Remaining  int64
	RetryAfter time.Duration
	Limit      int64
	Burst      int64
}

// RateLimitConfigProvider loads rate limit configuration for a tenant.
type RateLimitConfigProvider interface {
	RateLimitConfig(ctx context.Context, tenantID string) (RateLimitConfig, error)
}

// RateLimiter enforces per-tenant rate limits.
type RateLimiter interface {
	Allow(ctx context.Context, tenantID string, req RateLimitRequest) (RateLimitResult, error)
}

// InMemoryRateLimitManager stores per-tenant rate limit configs in memory.
type InMemoryRateLimitManager struct {
	mu      sync.RWMutex
	configs map[string]RateLimitConfig
}

// NewInMemoryRateLimitManager creates an in-memory rate limit config manager.
func NewInMemoryRateLimitManager() *InMemoryRateLimitManager {
	return &InMemoryRateLimitManager{
		configs: make(map[string]RateLimitConfig),
	}
}

// SetRateLimit sets rate limit config for a tenant.
func (p *InMemoryRateLimitManager) SetRateLimit(tenantID string, cfg RateLimitConfig) {
	if p == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.configs[tenantID] = cfg
}

// RateLimitConfig returns the rate limit config for a tenant.
// When the tenant has no explicit configuration, ErrTenantNotFound is returned.
// TokenBucketRateLimiter treats ErrTenantNotFound as "unlimited" so callers
// do not need to pre-populate every tenant to avoid being rate-limited.
func (p *InMemoryRateLimitManager) RateLimitConfig(ctx context.Context, tenantID string) (RateLimitConfig, error) {
	if p == nil {
		return RateLimitConfig{}, ErrTenantNotFound
	}
	p.mu.RLock()
	defer p.mu.RUnlock()
	cfg, ok := p.configs[tenantID]
	if !ok {
		return RateLimitConfig{}, ErrTenantNotFound
	}
	return cfg, nil
}

// TokenBucketRateLimiter implements per-tenant token bucket rate limiting.
type TokenBucketRateLimiter struct {
	provider RateLimitConfigProvider
	buckets  sync.Map // map[string]*tokenBucket
}

// NewTokenBucketRateLimiter creates a token bucket limiter from a config provider.
func NewTokenBucketRateLimiter(provider RateLimitConfigProvider) *TokenBucketRateLimiter {
	return &TokenBucketRateLimiter{provider: provider}
}

// DeleteTenant removes the token bucket for a tenant, reclaiming memory.
// Call this when a tenant is deleted or permanently deactivated.
func (l *TokenBucketRateLimiter) DeleteTenant(tenantID string) {
	if l != nil {
		l.buckets.Delete(tenantID)
	}
}

// Allow checks rate limits for a tenant. A new rate for an active bucket
// takes effect after refilling to the request time at the previous rate.
func (l *TokenBucketRateLimiter) Allow(ctx context.Context, tenantID string, req RateLimitRequest) (RateLimitResult, error) {
	if l == nil || l.provider == nil {
		return RateLimitResult{Allowed: true}, nil
	}
	if tenantID == "" {
		return RateLimitResult{Allowed: true}, nil
	}

	cfg, err := l.provider.RateLimitConfig(ctx, tenantID)
	if err != nil {
		if errors.Is(err, ErrTenantNotFound) {
			return RateLimitResult{Allowed: true}, nil
		}
		return RateLimitResult{Allowed: false}, err
	}

	if cfg.RequestsPerSecond <= 0 && cfg.Burst <= 0 {
		return RateLimitResult{Allowed: true}, nil
	}
	if cfg.RequestsPerSecond <= 0 {
		return RateLimitResult{Allowed: true}, nil
	}

	effectiveBurst := cfg.effectiveBurst()

	if req.Tokens <= 0 {
		req.Tokens = 1
	}
	if req.Now.IsZero() {
		req.Now = time.Now().UTC()
	}

	bucket := l.getBucket(tenantID)
	bucket.mu.Lock()
	defer bucket.mu.Unlock()

	if bucket.last.IsZero() {
		bucket.last = req.Now
		bucket.tokens = float64(effectiveBurst)
	}

	// Refill using the configuration that applied throughout the elapsed interval.
	if elapsed := req.Now.Sub(bucket.last).Seconds(); elapsed > 0 {
		bucket.tokens = min(float64(bucket.capacity), bucket.tokens+elapsed*float64(bucket.refillRate))
		bucket.last = req.Now
	}

	if bucket.capacity != effectiveBurst || bucket.refillRate != cfg.RequestsPerSecond {
		bucket.capacity = effectiveBurst
		bucket.refillRate = cfg.RequestsPerSecond
		bucket.tokens = min(float64(bucket.capacity), bucket.tokens)
	}

	allowed := bucket.tokens >= float64(req.Tokens)
	if allowed {
		bucket.tokens -= float64(req.Tokens)
	}

	retryAfter := time.Duration(0)
	if !allowed && bucket.refillRate > 0 {
		needed := float64(req.Tokens) - bucket.tokens
		if needed < 0 {
			needed = 0
		}
		retryAfter = time.Duration((needed / float64(bucket.refillRate)) * float64(time.Second))
		if retryAfter < 0 {
			retryAfter = 0
		}
	}

	result := RateLimitResult{
		Allowed:    allowed,
		Remaining:  int64(bucket.tokens),
		RetryAfter: retryAfter,
		Limit:      cfg.RequestsPerSecond,
		Burst:      effectiveBurst,
	}

	if !allowed {
		return result, ErrRateLimitExceeded
	}
	return result, nil
}

// tokenBucket is a local token bucket used by TokenBucketRateLimiter.
// It is intentionally kept separate from x/resilience/ratelimit.TokenBucket because
// it supports a caller-supplied "now" time for deterministic testing and
// in-place rate/capacity reconfiguration without losing accumulated tokens.
type tokenBucket struct {
	mu         sync.Mutex
	tokens     float64
	last       time.Time
	capacity   int64
	refillRate int64
}

func (l *TokenBucketRateLimiter) getBucket(tenantID string) *tokenBucket {
	if bucket, ok := l.buckets.Load(tenantID); ok {
		return bucket.(*tokenBucket)
	}
	newBucket := &tokenBucket{}
	loaded, _ := l.buckets.LoadOrStore(tenantID, newBucket)
	return loaded.(*tokenBucket)
}
