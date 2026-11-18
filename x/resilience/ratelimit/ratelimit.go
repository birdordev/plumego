// Package ratelimit provides a reusable token bucket rate limiter.
//
// This is the canonical rate-limit primitive used internally by x/messaging/pubsub,
// x/tenant/ratelimit, and x/ai/resilience. It is not an HTTP middleware — for
// HTTP-layer abuse protection use middleware/abuseguard, which wraps security/abuse.
//
// Example – global limit:
//
//	bucket := ratelimit.New(100, 200) // 100 req/s, burst 200
//	if bucket.Allow() {
//	    // proceed
//	}
//
// Example – per-key limit:
//
//	keyed := ratelimit.NewKeyed(50, 50) // 50 req/s per key, burst 50
//	if keyed.Allow("user-42") {
//	    // proceed
//	}
package ratelimit

import (
	"context"
	"sync"
	"time"
)

// TokenBucket implements a token bucket rate limiter.
// It is safe for concurrent use.
type TokenBucket struct {
	mu        sync.Mutex
	rate      float64   // tokens replenished per second
	burst     int64     // maximum token capacity
	tokens    float64   // current available tokens
	lastCheck time.Time // time of last token replenishment
}

// New returns a TokenBucket that replenishes at rate tokens/second with a
// maximum capacity (burst) of burst tokens. The bucket starts full.
func New(rate float64, burst int64) *TokenBucket {
	return &TokenBucket{
		rate:      rate,
		burst:     burst,
		tokens:    float64(burst),
		lastCheck: time.Now(),
	}
}

// Allow reports whether one token is available, consuming it if so.
func (b *TokenBucket) Allow() bool {
	return b.AllowN(1)
}

// AllowN reports whether n tokens are available, consuming them if so.
func (b *TokenBucket) AllowN(n int64) bool {
	if n <= 0 {
		return true
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.take(n)
}

// take consumes tokens with b.mu held.
func (b *TokenBucket) take(n int64) bool {
	b.replenish(time.Now())
	if b.tokens < float64(n) {
		return false
	}
	b.tokens -= float64(n)
	return true
}

// Remaining reports the whole-token capacity currently available.
func (b *TokenBucket) Remaining() int64 {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.replenish(time.Now())
	if b.tokens <= 0 {
		return 0
	}
	return int64(b.tokens)
}

// Wait blocks until one token is available or ctx is cancelled.
func (b *TokenBucket) Wait(ctx context.Context) error {
	return b.WaitN(ctx, 1)
}

// WaitN blocks until n tokens are available or ctx is canceled. Cancellation
// is checked under the lock before consuming tokens. Nonpositive n is a no-op.
func (b *TokenBucket) WaitN(ctx context.Context, n int64) error {
	if n <= 0 {
		return nil
	}
	timer := time.NewTimer(time.Millisecond)
	defer timer.Stop()
	for {
		b.mu.Lock()
		if err := ctx.Err(); err != nil {
			b.mu.Unlock()
			return err
		}
		available := b.take(n)
		b.mu.Unlock()
		if available {
			return nil
		}
		if !timer.Stop() {
			select {
			case <-timer.C:
			default:
			}
		}
		timer.Reset(time.Millisecond)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timer.C:
		}
	}
}

// UpdateRate updates the replenishment rate and maximum burst capacity.
func (b *TokenBucket) UpdateRate(rate float64, burst int64) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.updateRate(rate, burst, time.Now())
}

// updateRate settles the old rate before applying the new one. Hold b.mu.
func (b *TokenBucket) updateRate(rate float64, burst int64, now time.Time) {
	b.replenish(now)
	b.rate = rate
	if burst > 0 {
		b.burst = burst
		if b.tokens > float64(burst) {
			b.tokens = float64(burst)
		}
	}
}

// replenish adds tokens for elapsed time. Must be called with b.mu held.
func (b *TokenBucket) replenish(now time.Time) {
	elapsed := now.Sub(b.lastCheck).Seconds()
	if b.rate <= 0 {
		if elapsed > 0 {
			b.lastCheck = now
		}
		return
	}
	if elapsed > 0 {
		b.tokens += elapsed * b.rate
		if b.tokens > float64(b.burst) {
			b.tokens = float64(b.burst)
		}
		b.lastCheck = now
	}
}

// KeyedBuckets manages per-key token buckets with shared rate and burst settings.
// All keys share the same rate/burst configuration.
// It is safe for concurrent use.
type KeyedBuckets struct {
	rate  float64
	burst int64

	mu      sync.RWMutex
	buckets map[string]*TokenBucket
}

// NewKeyed returns a KeyedBuckets where each distinct key gets its own
// TokenBucket with the given rate and burst.
func NewKeyed(rate float64, burst int64) *KeyedBuckets {
	return &KeyedBuckets{
		rate:    rate,
		burst:   burst,
		buckets: make(map[string]*TokenBucket),
	}
}

// Allow reports whether one token is available for the given key, consuming it if so.
func (k *KeyedBuckets) Allow(key string) bool {
	return k.AllowN(key, 1)
}

// AllowN reports whether n tokens are available for the given key, consuming them if so.
func (k *KeyedBuckets) AllowN(key string, n int64) bool {
	return k.bucket(key).AllowN(n)
}

// Remaining reports the whole-token capacity currently available for the key.
func (k *KeyedBuckets) Remaining(key string) int {
	return int(k.bucket(key).Remaining())
}

// Wait blocks until one token is available for the given key or ctx is cancelled.
func (k *KeyedBuckets) Wait(ctx context.Context, key string) error {
	return k.bucket(key).WaitN(ctx, 1)
}

// UpdateRate updates the rate and burst for all existing and new buckets.
func (k *KeyedBuckets) UpdateRate(rate float64, burst int64) {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.rate = rate
	k.burst = burst
	for _, b := range k.buckets {
		b.UpdateRate(rate, burst)
	}
}

// Delete removes the bucket for key, reclaiming memory.
func (k *KeyedBuckets) Delete(key string) {
	k.mu.Lock()
	delete(k.buckets, key)
	k.mu.Unlock()
}

// Len returns the number of active key buckets.
func (k *KeyedBuckets) Len() int {
	k.mu.RLock()
	defer k.mu.RUnlock()
	return len(k.buckets)
}

func (k *KeyedBuckets) bucket(key string) *TokenBucket {
	k.mu.RLock()
	b, ok := k.buckets[key]
	k.mu.RUnlock()
	if ok {
		return b
	}
	k.mu.Lock()
	defer k.mu.Unlock()
	if b, ok = k.buckets[key]; ok {
		return b
	}
	b = New(k.rate, k.burst)
	k.buckets[key] = b
	return b
}
