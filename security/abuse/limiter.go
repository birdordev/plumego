// Package abuse provides rate limiting and anti-abuse protection.
//
// This package implements a high-performance token bucket rate limiter with:
//   - Per-key rate limiting (e.g., per IP, per user)
//   - Configurable burst capacity and refill rate
//   - Automatic cleanup of idle entries
//   - Sharded internal storage for reduced lock contention
//   - Memory-efficient design with configurable limits
//
// The token bucket algorithm allows controlled burst traffic while maintaining
// a steady rate over time, making it ideal for API rate limiting.
//
// Example usage:
//
//	import "github.com/spcent/plumego/security/abuse"
//
//	// Create limiter: 100 requests/sec with burst of 200
//	limiter, err := abuse.NewLimiterWithConfig(abuse.Config{
//		Rate:     100,  // tokens per second
//		Capacity: 200,  // burst capacity
//	})
//	if err != nil {
//		return err
//	}
//
//	// Check if request is allowed
//	clientIP := "192.168.1.1"
//	decision := limiter.Allow(clientIP)
//	if !decision.Allowed {
//		// Rate limit exceeded
//		http.Error(w, "Too Many Requests", http.StatusTooManyRequests)
//		return
//	}
//
//	// Process request...
package abuse

import (
	"errors"
	"fmt"
	"math"
	"sync"
	"sync/atomic"
	"time"
)

var (
	ErrInvalidConfig = errors.New("abuse: invalid limiter config")
	ErrInvalidKey    = errors.New("abuse: limiter key is required")
)

const (
	defaultRate            = 100
	defaultCapacity        = 200
	defaultCleanupInterval = time.Minute
	defaultMaxIdle         = 5 * time.Minute
	defaultMaxEntries      = 100000
	defaultShards          = 16
)

// Config controls the per-key limiter behavior.
//
// Config configures a token bucket rate limiter for each key (e.g., client IP).
// The token bucket algorithm allows burst traffic up to the capacity limit,
// then throttles requests to the specified rate.
//
// Example:
//
//	import "github.com/spcent/plumego/security/abuse"
//
//	config := abuse.Config{
//		Rate:            10.0,      // 10 requests per second
//		Capacity:        100,       // Burst capacity of 100 requests
//		CleanupInterval: time.Minute, // Clean up idle entries every minute
//		MaxIdle:         5 * time.Minute, // Remove entries idle for 5 minutes
//	}
//	limiter, err := abuse.NewLimiterWithConfig(config)
//	if err != nil {
//		return err
//	}
//
// The limiter automatically cleans up idle entries to prevent memory leaks.
type Config struct {
	// Rate is the number of requests per second allowed per key
	Rate float64

	// Capacity is the maximum burst size (number of tokens in the bucket)
	Capacity int

	// MaxEntries is the maximum number of tracked keys before eviction kicks in
	MaxEntries int

	// CleanupInterval is how often to clean up idle entries
	CleanupInterval time.Duration

	// MaxIdle is the maximum time an entry can be idle before being removed
	MaxIdle time.Duration

	// Shards is the number of lock shards for buckets
	Shards int

	// Now is a function that returns the current time (for testing)
	Now func() time.Time
}

// Decision captures the outcome of a limiter check.
//
// Example:
//
//	import "github.com/spcent/plumego/security/abuse"
//
//	limiter := abuse.NewLimiter(abuse.DefaultConfig())
//	decision := limiter.Allow("192.168.1.1")
//	if !decision.Allowed {
//		fmt.Printf("Rate limited. Retry after: %v\n", decision.RetryAfter)
//	}
type Decision struct {
	// Allowed indicates whether the request is allowed
	Allowed bool

	// Limit is the maximum number of requests allowed
	Limit int

	// Remaining is the number of requests remaining in the current window
	Remaining int

	// Reset is the time when the rate limit resets
	Reset time.Time

	// RetryAfter is the duration to wait before retrying (when not allowed)
	RetryAfter time.Duration
}

// Limiter enforces a token bucket per key.
//
// Limiter provides per-key rate limiting using a token bucket algorithm.
// Each key (e.g., client IP) has its own token bucket that refills at the
// specified rate up to the capacity limit.
//
// Example:
//
//	import "github.com/spcent/plumego/security/abuse"
//
//	config := abuse.Config{
//		Rate:     10.0,  // 10 requests per second
//		Capacity: 100,   // Burst capacity of 100
//	}
//	limiter, err := abuse.NewLimiterWithConfig(config)
//	if err != nil {
//		return err
//	}
//	defer limiter.Stop()
//
//	// Check if request is allowed
//	decision := limiter.Allow("192.168.1.1")
//	if !decision.Allowed {
//		// Reject request
//	}
//
// The limiter automatically cleans up idle entries to prevent memory leaks.
// Call Stop() when done to clean up resources.
type shardCounter struct {
	counters []atomic.Int64
}

func (sc *shardCounter) Add(shardIdx int, delta int64) {
	if shardIdx < 0 || shardIdx >= len(sc.counters) {
		return
	}
	sc.counters[shardIdx].Add(delta)
}

func (sc *shardCounter) Total() int64 {
	var total int64
	for i := range sc.counters {
		total += sc.counters[i].Load()
	}
	return total
}

func (sc *shardCounter) Load() int64 {
	return sc.Total()
}

// LimiterMetrics holds monitoring counters for a Limiter.
//
// Allowed, Rejected, and Evictions are cumulative counters that increase
// monotonically since the limiter was created. Buckets is a point-in-time
// snapshot of the current number of tracked keys; it rises and falls as keys
// are added and evicted.
type LimiterMetrics struct {
	// Allowed is the cumulative number of requests that passed the rate limit.
	Allowed int64
	// Rejected is the cumulative number of requests that were denied.
	Rejected int64
	// Evictions is the cumulative number of bucket entries evicted due to
	// MaxEntries pressure or idle expiry.
	Evictions int64
	// Buckets is the current number of active per-key buckets (point-in-time snapshot).
	Buckets int64
}

type limiterMetricCounters struct {
	Allowed   atomic.Int64
	Rejected  atomic.Int64
	Evictions atomic.Int64
	Buckets   atomic.Int64
}

type Limiter struct {
	shards          []limiterShard
	rate            float64
	capacity        float64
	cleanupInterval time.Duration
	maxIdle         time.Duration
	maxEntries      int
	now             func() time.Time
	stopCh          chan struct{}
	stoppedCh       chan struct{}
	startOnce       sync.Once
	stopOnce        sync.Once
	bucketCount     shardCounter
	// Metrics for monitoring
	metrics limiterMetricCounters
}

type limiterShard struct {
	mu      sync.RWMutex
	buckets map[string]*bucket
}

type bucket struct {
	tokens     float64
	lastRefill time.Time
	lastAccess time.Time
}

// DefaultConfig returns baseline limiter settings.
func DefaultConfig() Config {
	return Config{
		Rate:            defaultRate,
		Capacity:        defaultCapacity,
		MaxEntries:      defaultMaxEntries,
		CleanupInterval: defaultCleanupInterval,
		MaxIdle:         defaultMaxIdle,
		Shards:          defaultShards,
	}
}

// Validate checks if the configuration is valid.
//
// Zero fields are treated as omitted and filled from DefaultConfig by
// NewLimiterWithConfig. Explicit negative values are invalid.
func (c Config) Validate() error {
	_, err := normalizeConfig(c)
	return err
}

// NewLimiter creates a limiter.
//
// NewLimiter fails closed for invalid explicit configuration by returning a
// disabled limiter that denies requests. Use NewLimiterWithConfig when the
// caller needs the configuration error.
//
// Example:
//
//	import "github.com/spcent/plumego/security/abuse"
//
//	config := abuse.Config{
//		Rate:     10.0,  // 10 requests per second
//		Capacity: 100,   // Burst capacity of 100
//	}
//	limiter := abuse.NewLimiter(config)
//	defer limiter.Stop()
func NewLimiter(config Config) *Limiter {
	limiter, err := NewLimiterWithConfig(config)
	if err != nil {
		// Fail closed as documented: return a disabled limiter that denies all
		// requests rather than panicking on an invalid explicit configuration.
		return &Limiter{}
	}
	return limiter
}

// NewLimiterWithConfig creates a limiter with strict startup semantics.
//
// This is the canonical production constructor. It returns configuration errors
// for explicitly invalid non-zero values. Zero fields are treated as omitted and
// are filled from DefaultConfig.
func NewLimiterWithConfig(config Config) (*Limiter, error) {
	normalized, err := normalizeConfig(config)
	if err != nil {
		return nil, err
	}

	limiter := &Limiter{
		shards:          make([]limiterShard, normalized.Shards),
		rate:            normalized.Rate,
		capacity:        float64(normalized.Capacity),
		cleanupInterval: normalized.CleanupInterval,
		maxIdle:         normalized.MaxIdle,
		maxEntries:      normalized.MaxEntries,
		now:             normalized.Now,
		stopCh:          make(chan struct{}),
		stoppedCh:       make(chan struct{}),
	}

	limiter.bucketCount.counters = make([]atomic.Int64, normalized.Shards)

	for i := range limiter.shards {
		limiter.shards[i].buckets = make(map[string]*bucket)
	}

	limiter.startCleanup()

	return limiter, nil
}

func normalizeConfig(config Config) (Config, error) {
	defaults := DefaultConfig()

	if config.Rate < 0 {
		return Config{}, fmt.Errorf("%w: rate must be positive, got %f", ErrInvalidConfig, config.Rate)
	}
	if config.Capacity < 0 {
		return Config{}, fmt.Errorf("%w: capacity must be positive, got %d", ErrInvalidConfig, config.Capacity)
	}
	if config.MaxEntries < 0 {
		return Config{}, fmt.Errorf("%w: max_entries must be positive, got %d", ErrInvalidConfig, config.MaxEntries)
	}
	if config.CleanupInterval < 0 {
		return Config{}, fmt.Errorf("%w: cleanup_interval must be positive, got %v", ErrInvalidConfig, config.CleanupInterval)
	}
	if config.MaxIdle < 0 {
		return Config{}, fmt.Errorf("%w: max_idle must be positive, got %v", ErrInvalidConfig, config.MaxIdle)
	}
	if config.Shards < 0 {
		return Config{}, fmt.Errorf("%w: shards must be positive, got %d", ErrInvalidConfig, config.Shards)
	}
	if config.Rate == 0 {
		config.Rate = defaults.Rate
	}
	if config.Capacity == 0 {
		config.Capacity = defaults.Capacity
	}
	if config.MaxEntries == 0 {
		config.MaxEntries = defaults.MaxEntries
	}
	if config.CleanupInterval == 0 {
		config.CleanupInterval = defaults.CleanupInterval
	}
	if config.MaxIdle == 0 {
		config.MaxIdle = defaults.MaxIdle
	}
	if config.Shards == 0 {
		config.Shards = defaults.Shards
	}
	if config.Now == nil {
		config.Now = time.Now
	}
	return config, nil
}

// Allow checks and consumes a token for the given key.
func (l *Limiter) Allow(key string) Decision {
	if !l.ready() {
		l.RecordAllow(false)
		return Decision{Allowed: false}
	}
	if key == "" {
		l.RecordAllow(false)
		return Decision{Allowed: false}
	}

	now := l.now()
	if l.maxEntries > 0 && l.bucketCount.Load() >= int64(l.maxEntries) {
		l.evictOldestGlobal()
	}
	shard := l.shardFor(key)
	shardIdx := fnv32a(key) % uint32(len(l.shards))

	// --- critical section: update bucket state only ---
	shard.mu.Lock()
	b, ok := shard.buckets[key]
	if !ok {
		if l.maxEntries > 0 && l.bucketCount.Load() >= int64(l.maxEntries) {
			l.evictOldestLocked(shard, int(shardIdx))
		}
		b = &bucket{
			tokens:     l.capacity,
			lastRefill: now,
			lastAccess: now,
		}
		shard.buckets[key] = b
		l.bucketCount.Add(int(shardIdx), 1)
		l.updateBucketMetric()
	}

	elapsed := now.Sub(b.lastRefill).Seconds()
	if elapsed < 0 {
		elapsed = 0
	}
	tokens := math.Min(l.capacity, b.tokens+elapsed*l.rate)
	allowed := tokens >= 1
	if allowed {
		tokens -= 1
	}
	b.tokens = tokens
	b.lastRefill = now
	b.lastAccess = now
	shard.mu.Unlock()
	// --- end critical section ---

	// Build decision outside the lock with local snapshot values.
	now = l.now() // refresh after unlock for time-sensitive fields
	decision := Decision{
		Allowed:   allowed,
		Limit:     int(l.capacity),
		Remaining: int(math.Floor(tokens)),
		Reset:     now,
	}

	if !allowed && l.rate > 0 {
		need := 1 - tokens
		if need < 0 {
			need = 0
		}
		decision.RetryAfter = durationFromSeconds(need / l.rate)
	}

	if l.rate > 0 {
		missing := l.capacity - tokens
		if missing < 0 {
			missing = 0
		}
		decision.Reset = now.Add(durationFromSeconds(missing / l.rate))
	}

	l.RecordAllow(allowed)
	return decision
}

// AllowKey checks and consumes a token for a non-empty limiter key.
// It returns ErrInvalidKey when the key is empty.
func (l *Limiter) AllowKey(key string) (Decision, error) {
	if key == "" {
		if l != nil {
			l.RecordAllow(false)
		}
		return Decision{Allowed: false}, ErrInvalidKey
	}
	return l.Allow(key), nil
}

// Stop stops the cleanup loop.
func (l *Limiter) Stop() {
	if !l.ready() {
		return
	}
	l.stopOnce.Do(func() {
		close(l.stopCh)
		<-l.stoppedCh
	})
}

func (l *Limiter) startCleanup() {
	l.startOnce.Do(func() {
		go l.cleanupLoop()
	})
}

func (l *Limiter) cleanupLoop() {
	ticker := time.NewTicker(l.cleanupInterval)
	defer ticker.Stop()
	defer close(l.stoppedCh)

	for {
		select {
		case <-ticker.C:
			l.cleanup(l.now())
		case <-l.stopCh:
			return
		}
	}
}

func (l *Limiter) cleanup(now time.Time) {
	if !l.ready() {
		return
	}
	for i := range l.shards {
		shard := &l.shards[i]
		shard.mu.Lock()
		for key, b := range shard.buckets {
			if now.Sub(b.lastAccess) > l.maxIdle {
				delete(shard.buckets, key)
				l.bucketCount.Add(i, -1)
				l.updateBucketMetric()
			}
		}
		shard.mu.Unlock()
	}
}

func (l *Limiter) shardFor(key string) *limiterShard {
	if len(l.shards) == 1 {
		return &l.shards[0]
	}
	idx := fnv32a(key) % uint32(len(l.shards))
	return &l.shards[idx]
}

func (l *Limiter) evictOldestLocked(shard *limiterShard, shardIdx int) {
	var oldestKey string
	var oldestTime time.Time
	first := true
	for key, b := range shard.buckets {
		if first || b.lastAccess.Before(oldestTime) {
			oldestKey = key
			oldestTime = b.lastAccess
			first = false
		}
	}
	if oldestKey != "" {
		delete(shard.buckets, oldestKey)
		l.bucketCount.Add(shardIdx, -1)
		l.metrics.Evictions.Add(1)
		l.updateBucketMetric()
	}
}

func (l *Limiter) evictOldestGlobal() {
	if !l.ready() {
		return
	}
	var oldestShard *limiterShard
	var oldestKey string
	var oldestTime time.Time
	first := true

	// Phase 1: Find the oldest entry across all shards
	for i := range l.shards {
		shard := &l.shards[i]
		shard.mu.Lock()
		for key, b := range shard.buckets {
			if first || b.lastAccess.Before(oldestTime) {
				oldestKey = key
				oldestTime = b.lastAccess
				oldestShard = shard
				first = false
			}
		}
		shard.mu.Unlock()
	}

	if oldestShard == nil || oldestKey == "" {
		return
	}

	// Phase 2: Double-check and remove the oldest entry
	// This prevents TOCTOU race where the entry might have been modified
	oldestShard.mu.Lock()
	if bucket, exists := oldestShard.buckets[oldestKey]; exists && bucket.lastAccess.Equal(oldestTime) {
		delete(oldestShard.buckets, oldestKey)
		shardIdx := fnv32a(oldestKey) % uint32(len(l.shards))
		l.bucketCount.Add(int(shardIdx), -1)
		l.metrics.Evictions.Add(1)
		l.updateBucketMetric()
	}
	oldestShard.mu.Unlock()
}

// Metrics returns a point-in-time metrics snapshot for monitoring.
func (l *Limiter) Metrics() LimiterMetrics {
	if l == nil {
		return LimiterMetrics{}
	}
	return LimiterMetrics{
		Allowed:   l.metrics.Allowed.Load(),
		Rejected:  l.metrics.Rejected.Load(),
		Evictions: l.metrics.Evictions.Load(),
		Buckets:   l.metrics.Buckets.Load(),
	}
}

// RecordAllow records the outcome of an Allow check for metrics
func (l *Limiter) RecordAllow(allowed bool) {
	if l == nil {
		return
	}
	if allowed {
		l.metrics.Allowed.Add(1)
	} else {
		l.metrics.Rejected.Add(1)
	}
}

func (l *Limiter) updateBucketMetric() {
	l.metrics.Buckets.Store(l.bucketCount.Load())
}

func (l *Limiter) ready() bool {
	return l != nil && len(l.shards) > 0 && l.now != nil && l.stopCh != nil && l.stoppedCh != nil
}

func durationFromSeconds(seconds float64) time.Duration {
	if seconds <= 0 {
		return 0
	}
	const maxDuration = time.Duration(1<<63 - 1)
	maxSeconds := float64(maxDuration) / float64(time.Second)
	if seconds > maxSeconds {
		return maxDuration
	}
	return time.Duration(seconds * float64(time.Second))
}

func fnv32a(key string) uint32 {
	const (
		offset32 = 2166136261
		prime32  = 16777619
	)
	hash := uint32(offset32)
	for i := 0; i < len(key); i++ {
		hash ^= uint32(key[i])
		hash *= prime32
	}
	return hash
}
