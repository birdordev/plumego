// Package cache provides a small in-memory cache primitive with TTL-aware and
// memory-bounded eviction.
//
// This package implements an in-process cache primitive with features including:
//   - TTL (time-to-live) expiration for entries
//   - memory-bounded eviction
//   - thread-safe operations
//
// For distributed, Redis-backed, or topology-heavy caching, see x/data/cache.
//
// Example usage:
//
//	import (
//		"context"
//		"time"
//
//		"github.com/birdordev/plumego/store/cache"
//	)
//
//	func example() error {
//		ctx := context.Background()
//		userData := []byte("profile")
//
//		c, err := cache.NewMemoryCacheWithConfig(cache.Config{
//			MaxMemoryUsage: 10 << 20, // 10 MiB
//			DefaultTTL:     5 * time.Minute,
//		})
//		if err != nil {
//			return err
//		}
//		defer c.Close()
//
//		if err := c.Set(ctx, "user:123", userData, 0); err != nil {
//			return err
//		}
//		if val, err := c.Get(ctx, "user:123"); err == nil {
//			_ = val
//		}
//		return c.Delete(ctx, "user:123")
//	}
package cache

import (
	"context"
	"errors"
	"time"
)

var (
	// ErrNotFound is returned when a cache entry is missing or expired.
	ErrNotFound = errors.New("cache: key not found")

	// ErrInvalidConfig is returned when configuration is invalid.
	ErrInvalidConfig = errors.New("cache: invalid config")

	// ErrCacheFull is returned when cache reaches capacity limit.
	ErrCacheFull = errors.New("cache: cache full")

	// ErrKeyTooLong is returned when cache key exceeds maximum length.
	ErrKeyTooLong = errors.New("cache: key too long")

	// ErrInvalidKey is returned when a cache key is empty or unsafe.
	ErrInvalidKey = errors.New("cache: key is required")

	// ErrNotInteger is returned when attempting increment/decrement on non-integer value.
	ErrNotInteger = errors.New("cache: value is not an integer")

	// ErrCacheClosed is returned when operating on a closed cache.
	ErrCacheClosed = errors.New("cache: cache is closed")

	// ErrCapabilityUnsupported is returned when an optional cache capability is unavailable.
	ErrCapabilityUnsupported = errors.New("cache: capability unsupported")
)

// Cache defines the minimal contract for cache backends.
type Cache interface {
	Get(ctx context.Context, key string) ([]byte, error)
	Set(ctx context.Context, key string, value []byte, ttl time.Duration) error
	Delete(ctx context.Context, key string) error
	Exists(ctx context.Context, key string) (bool, error)
	Clear(ctx context.Context) error
}

// CounterCache is implemented by cache backends that support atomic integer counters.
type CounterCache interface {
	// Incr increments the integer value of a key by delta.
	// Returns the new value after increment.
	// If the key doesn't exist, it's created with delta as the initial value.
	// Returns ErrNotInteger if the value is not an integer.
	Incr(ctx context.Context, key string, delta int64) (int64, error)

	// Decr decrements the integer value of a key by delta.
	// Returns the new value after decrement.
	// If the key doesn't exist, it's created with -delta as the initial value.
	// Returns ErrNotInteger if the value is not an integer.
	Decr(ctx context.Context, key string, delta int64) (int64, error)
}

// AppenderCache is implemented by cache backends that support byte append operations.
type AppenderCache interface {
	// Append appends data to the end of an existing value.
	// If the key doesn't exist, it's created with the data as the value.
	Append(ctx context.Context, key string, data []byte) error
}

// AtomicCache combines the base cache contract with optional atomic operations.
type AtomicCache interface {
	Cache
	CounterCache
	AppenderCache
}
