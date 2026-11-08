// Package webhookin provides webhook receiver functionality with signature verification.
//
// This package implements secure webhook handling for popular providers including:
//   - GitHub webhooks with HMAC-SHA256 verification
//   - Stripe webhooks with timestamp-based signatures
//   - Generic HMAC webhook verification
//   - Deduplication to prevent replay attacks
//   - Request validation and parsing
//
// All signature verification uses constant-time comparison to prevent timing attacks.
// The package follows security best practices for webhook handling.
//
// Example usage:
//
//	import "github.com/birdordev/plumego/x/messaging/webhook"
//
//	// Verify GitHub webhook
//	secret := []byte("your-webhook-secret")
//	payload, err := webhookin.VerifyGitHub(r, secret)
//	if err != nil {
//		// Invalid signature or request
//	}
//
//	// Verify Stripe webhook
//	event, err := webhookin.VerifyStripe(r, stripeSecret)
//	if err != nil {
//		// Invalid signature
//	}
//
//	// Use deduplication to prevent replays
//	dedup := webhookin.NewDeduper(10 * time.Minute)
//	if !dedup.Allow(requestID) {
//		// Duplicate request
//	}
package webhook

import (
	"sync"
	"time"
)

// Deduper is an in-memory TTL-based idempotency gate.
// It is stdlib-only and safe for concurrent use.
type Deduper struct {
	mu    sync.RWMutex
	items map[string]time.Time // key -> expiresAt
	ttl   time.Duration

	// best-effort cleanup cadence; no background goroutine required
	lastSweep     time.Time
	sweepInterval time.Duration
}

// NewDeduper creates a new deduplication gate with specified TTL.
// If ttl <= 0, defaults to 10 minutes.
func NewDeduper(ttl time.Duration) *Deduper {
	if ttl <= 0 {
		ttl = 10 * time.Minute
	}
	return &Deduper{
		items:         make(map[string]time.Time),
		ttl:           ttl,
		lastSweep:     time.Now().UTC(),
		sweepInterval: 1 * time.Minute,
	}
}

// WithSweepInterval sets the interval between cleanup sweeps.
func (d *Deduper) WithSweepInterval(interval time.Duration) *Deduper {
	if interval > 0 {
		d.sweepInterval = interval
	}
	return d
}

// SeenBefore marks key as seen and returns true if it was already present and not expired.
// Thread-safe and uses read lock for lookups.
func (d *Deduper) SeenBefore(key string) bool {
	now := time.Now().UTC()

	// Fast path: read lock for lookup
	d.mu.RLock()
	exp, exists := d.items[key]
	d.mu.RUnlock()

	if exists && now.Before(exp) {
		return true
	}

	// Slow path: write lock for insertion and cleanup
	d.mu.Lock()
	defer d.mu.Unlock()

	// Double-check after acquiring write lock
	if exp, ok := d.items[key]; ok && now.Before(exp) {
		return true
	}

	// Periodic cleanup to avoid unbounded growth
	if now.Sub(d.lastSweep) > d.sweepInterval {
		for k, exp := range d.items {
			if now.After(exp) {
				delete(d.items, k)
			}
		}
		d.lastSweep = now
	}

	d.items[key] = now.Add(d.ttl)
	return false
}

// Clear removes all expired entries.
func (d *Deduper) Clear() {
	d.mu.Lock()
	defer d.mu.Unlock()

	now := time.Now().UTC()
	for k, exp := range d.items {
		if now.After(exp) {
			delete(d.items, k)
		}
	}
}

// Count returns the number of active (non-expired) entries.
func (d *Deduper) Count() int {
	d.mu.RLock()
	defer d.mu.RUnlock()

	now := time.Now().UTC()
	count := 0
	for _, exp := range d.items {
		if now.Before(exp) {
			count++
		}
	}
	return count
}
