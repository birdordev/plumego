package pubsub

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	"github.com/spcent/plumego/x/resilience/ratelimit"
)

// Rate limit errors

// RateLimitConfig configures rate limiting
type RateLimitConfig struct {
	// GlobalQPS is the global queries per second limit (0 = unlimited)
	GlobalQPS int

	// GlobalBurst is the maximum burst size for global limit
	GlobalBurst int

	// PerTopicQPS is the per-topic QPS limit (0 = unlimited)
	PerTopicQPS int

	// PerTopicBurst is the maximum burst size for per-topic limit
	PerTopicBurst int

	// PerSubscriberQPS is the per-subscriber QPS limit (0 = unlimited)
	PerSubscriberQPS int

	// PerSubscriberBurst is the maximum burst size for per-subscriber limit
	PerSubscriberBurst int

	// Adaptive enables adaptive rate limiting based on system load
	Adaptive bool

	// AdaptiveTarget is the target system utilization (0.0-1.0)
	AdaptiveTarget float64

	// AdaptiveAdjustInterval is how often to adjust adaptive limits
	AdaptiveAdjustInterval time.Duration

	// WaitOnLimit if true, blocks until token available; if false, returns error
	WaitOnLimit bool

	// WaitTimeout maximum time to wait for token (only if WaitOnLimit=true)
	WaitTimeout time.Duration
}

// DefaultRateLimitConfig returns default rate limit configuration
func DefaultRateLimitConfig() RateLimitConfig {
	return RateLimitConfig{
		GlobalQPS:              0, // unlimited
		GlobalBurst:            100,
		PerTopicQPS:            0, // unlimited
		PerTopicBurst:          50,
		PerSubscriberQPS:       0, // unlimited
		PerSubscriberBurst:     10,
		Adaptive:               false,
		AdaptiveTarget:         0.8,
		AdaptiveAdjustInterval: 10 * time.Second,
		WaitOnLimit:            false,
		WaitTimeout:            1 * time.Second,
	}
}

// RateLimitedPubSub wraps InProcBroker with rate limiting
type RateLimitedPubSub struct {
	*InProcBroker

	config RateLimitConfig

	// Global rate limiter
	globalLimiter *tokenBucket

	// Per-topic rate limiters
	topicLimiters   map[string]*tokenBucket
	topicLimitersMu sync.RWMutex

	// Per-subscriber rate limiters
	subLimiters   map[uint64]*tokenBucket
	subLimitersMu sync.RWMutex

	// Adaptive rate limiting
	currentLoad    atomic.Value // float64
	adaptiveFactor atomic.Value // float64

	// Background workers
	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
	closed atomic.Bool

	// Metrics
	limitExceeded atomic.Uint64
	limitWaited   atomic.Uint64
	adaptiveAdj   atomic.Uint64
}

// tokenBucket wraps x/resilience/ratelimit.TokenBucket adding per-bucket statistics.
type tokenBucket struct {
	*ratelimit.TokenBucket

	allowed atomic.Uint64
	denied  atomic.Uint64
	waited  atomic.Uint64
}

// NewRateLimited creates a new rate-limited pubsub instance
func NewRateLimited(config RateLimitConfig, opts ...Option) (*RateLimitedPubSub, error) {
	// Validate config
	if config.GlobalQPS < 0 || config.PerTopicQPS < 0 || config.PerSubscriberQPS < 0 {
		return nil, ErrInvalidRateLimit
	}

	// Apply defaults
	if config.GlobalBurst == 0 && config.GlobalQPS > 0 {
		config.GlobalBurst = config.GlobalQPS
	}
	if config.PerTopicBurst == 0 && config.PerTopicQPS > 0 {
		config.PerTopicBurst = config.PerTopicQPS
	}
	if config.PerSubscriberBurst == 0 && config.PerSubscriberQPS > 0 {
		config.PerSubscriberBurst = config.PerSubscriberQPS
	}
	if config.WaitTimeout == 0 {
		config.WaitTimeout = 1 * time.Second
	}
	if config.AdaptiveAdjustInterval == 0 {
		config.AdaptiveAdjustInterval = 10 * time.Second
	}

	// Create base pubsub
	ps := New(opts...)

	ctx, cancel := newBackgroundLifecycle()

	rlps := &RateLimitedPubSub{
		InProcBroker:  ps,
		config:        config,
		topicLimiters: make(map[string]*tokenBucket),
		subLimiters:   make(map[uint64]*tokenBucket),
		ctx:           ctx,
		cancel:        cancel,
	}

	// Initialize adaptive values
	rlps.currentLoad.Store(0.0)
	rlps.adaptiveFactor.Store(1.0)

	// Create global limiter
	if config.GlobalQPS > 0 {
		rlps.globalLimiter = newTokenBucket(float64(config.GlobalQPS), config.GlobalBurst)
	}

	// Start adaptive worker if enabled
	if config.Adaptive {
		rlps.startAdaptiveWorker()
	}

	return rlps, nil
}

func newTokenBucket(rate float64, burst int) *tokenBucket {
	return &tokenBucket{TokenBucket: ratelimit.New(rate, int64(burst))}
}

func (tb *tokenBucket) allow() bool {
	if tb.Allow() {
		tb.allowed.Add(1)
		return true
	}
	tb.denied.Add(1)
	return false
}

func (tb *tokenBucket) wait(ctx context.Context, timeout time.Duration) error {
	ctx2, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	if err := tb.Wait(ctx2); err != nil {
		return ErrRateLimitExceeded
	}
	tb.waited.Add(1)
	return nil
}

func (tb *tokenBucket) updateRate(newRate float64) {
	tb.UpdateRate(newRate, 0)
}

// Publish publishes a message with rate limiting
func (rlps *RateLimitedPubSub) Publish(topic string, msg Message) error {
	if rlps.closed.Load() {
		return newErr(ErrCodeClosed, "publish", topic, "broker is closed", nil)
	}

	// Check global rate limit
	if rlps.globalLimiter != nil {
		if !rlps.checkOrWaitLimit(rlps.globalLimiter) {
			rlps.limitExceeded.Add(1)
			return ErrRateLimitExceeded
		}
	}

	// Check per-topic rate limit
	if rlps.config.PerTopicQPS > 0 {
		limiter := rlps.getOrCreateTopicLimiter(topic)
		if !rlps.checkOrWaitLimit(limiter) {
			rlps.limitExceeded.Add(1)
			return ErrRateLimitExceeded
		}
	}

	// Publish to base pubsub
	return rlps.InProcBroker.Publish(topic, msg)
}

// checkOrWaitLimit checks rate limit or waits if configured
func (rlps *RateLimitedPubSub) checkOrWaitLimit(limiter *tokenBucket) bool {
	if rlps.config.WaitOnLimit {
		ctx, cancel := context.WithTimeout(rlps.ctx, rlps.config.WaitTimeout)
		defer cancel()

		if err := limiter.wait(ctx, rlps.config.WaitTimeout); err == nil {
			rlps.limitWaited.Add(1)
			return true
		}
		return false
	}

	return limiter.allow()
}

// getOrCreateTopicLimiter gets or creates a rate limiter for a topic
func (rlps *RateLimitedPubSub) getOrCreateTopicLimiter(topic string) *tokenBucket {
	rlps.topicLimitersMu.RLock()
	limiter, exists := rlps.topicLimiters[topic]
	rlps.topicLimitersMu.RUnlock()

	if exists {
		return limiter
	}

	rlps.topicLimitersMu.Lock()
	defer rlps.topicLimitersMu.Unlock()

	// Double-check
	if limiter, exists = rlps.topicLimiters[topic]; exists {
		return limiter
	}

	// Create new limiter with adaptive rate
	rate := float64(rlps.config.PerTopicQPS)
	if rlps.config.Adaptive {
		factor := rlps.adaptiveFactor.Load().(float64)
		rate *= factor
	}

	limiter = newTokenBucket(rate, rlps.config.PerTopicBurst)
	rlps.topicLimiters[topic] = limiter

	return limiter
}

// getOrCreateSubLimiter gets or creates a rate limiter for a subscriber
func (rlps *RateLimitedPubSub) getOrCreateSubLimiter(subID uint64) *tokenBucket {
	rlps.subLimitersMu.RLock()
	limiter, exists := rlps.subLimiters[subID]
	rlps.subLimitersMu.RUnlock()

	if exists {
		return limiter
	}

	rlps.subLimitersMu.Lock()
	defer rlps.subLimitersMu.Unlock()

	// Double-check
	if limiter, exists = rlps.subLimiters[subID]; exists {
		return limiter
	}

	// Create new limiter
	rate := float64(rlps.config.PerSubscriberQPS)
	if rlps.config.Adaptive {
		factor := rlps.adaptiveFactor.Load().(float64)
		rate *= factor
	}

	limiter = newTokenBucket(rate, rlps.config.PerSubscriberBurst)
	rlps.subLimiters[subID] = limiter

	return limiter
}

// startAdaptiveWorker starts the adaptive rate limiting worker
func (rlps *RateLimitedPubSub) startAdaptiveWorker() {
	rlps.wg.Add(1)
	go func() {
		defer rlps.wg.Done()

		ticker := time.NewTicker(rlps.config.AdaptiveAdjustInterval)
		defer ticker.Stop()

		for {
			select {
			case <-ticker.C:
				rlps.adjustAdaptiveRates()

			case <-rlps.ctx.Done():
				return
			}
		}
	}()
}

// adjustAdaptiveRates adjusts rate limits based on system load
func (rlps *RateLimitedPubSub) adjustAdaptiveRates() {
	// Calculate current load (simplified metric)
	snapshot := rlps.Snapshot()

	// Use total subscribers across all topics as a proxy for load
	totalSubs := 0
	for _, tm := range snapshot.Topics {
		totalSubs += tm.SubscribersGauge
	}

	maxSubs := float64(rlps.config.GlobalBurst * 10) // arbitrary max
	var load float64
	if maxSubs > 0 {
		load = float64(totalSubs) / maxSubs
		if load > 1.0 {
			load = 1.0
		}
	}

	rlps.currentLoad.Store(load)

	// Calculate adaptive factor
	target := rlps.config.AdaptiveTarget
	var factor float64

	if load > target {
		// System overloaded - reduce rates
		overage := load - target
		factor = 1.0 - (overage * 0.5) // Reduce up to 50%
		if factor < 0.1 {
			factor = 0.1 // Don't go below 10%
		}
	} else {
		// System underutilized - can increase rates
		factor = 1.0
	}

	rlps.adaptiveFactor.Store(factor)
	rlps.adaptiveAdj.Add(1)

	// Update existing limiters
	if rlps.config.PerTopicQPS > 0 {
		newRate := float64(rlps.config.PerTopicQPS) * factor

		rlps.topicLimitersMu.RLock()
		for _, limiter := range rlps.topicLimiters {
			limiter.updateRate(newRate)
		}
		rlps.topicLimitersMu.RUnlock()
	}

	if rlps.config.PerSubscriberQPS > 0 {
		newRate := float64(rlps.config.PerSubscriberQPS) * factor

		rlps.subLimitersMu.RLock()
		for _, limiter := range rlps.subLimiters {
			limiter.updateRate(newRate)
		}
		rlps.subLimitersMu.RUnlock()
	}
}

// Subscribe creates a rate-limited subscription
func (rlps *RateLimitedPubSub) Subscribe(ctx context.Context, topic string, opts SubOptions) (Subscription, error) {
	sub, err := rlps.InProcBroker.Subscribe(ctx, topic, opts)
	if err != nil {
		return nil, err
	}

	// Wrap with rate limiting if per-subscriber limit enabled
	if rlps.config.PerSubscriberQPS > 0 {
		return &rateLimitedSubscription{
			Subscription: sub,
			limiter:      rlps.getOrCreateSubLimiter(sub.ID()),
			rlps:         rlps,
		}, nil
	}

	return sub, nil
}

// rateLimitedSubscription wraps a subscription with rate limiting
type rateLimitedSubscription struct {
	Subscription
	limiter *tokenBucket
	rlps    *RateLimitedPubSub
}

// C returns the rate-limited message channel
func (rls *rateLimitedSubscription) C() <-chan Message {
	// Create wrapper channel
	ch := make(chan Message, cap(rls.Subscription.C()))

	go func() {
		defer close(ch)

		for msg := range rls.Subscription.C() {
			// Check rate limit before forwarding
			if rls.rlps.config.WaitOnLimit {
				ctx, cancel := context.WithTimeout(context.Background(), rls.rlps.config.WaitTimeout)
				_ = rls.limiter.wait(ctx, rls.rlps.config.WaitTimeout)
				cancel()
			} else if !rls.limiter.allow() {
				rls.rlps.limitExceeded.Add(1)
				continue // Drop message
			}

			// Forward message
			select {
			case ch <- msg:
			case <-rls.rlps.ctx.Done():
				return
			}
		}
	}()

	return ch
}

// Close closes the rate-limited pubsub
func (rlps *RateLimitedPubSub) Close() error {
	if rlps.closed.Swap(true) {
		return nil
	}

	// Stop workers
	stopBackground(rlps.cancel, &rlps.wg)

	// Close base pubsub
	return rlps.InProcBroker.Close()
}

// RateLimitStats returns rate limiting statistics
func (rlps *RateLimitedPubSub) RateLimitStats() RateLimitStats {
	stats := RateLimitStats{
		LimitExceeded: rlps.limitExceeded.Load(),
		LimitWaited:   rlps.limitWaited.Load(),
		AdaptiveAdj:   rlps.adaptiveAdj.Load(),
	}

	if rlps.config.Adaptive {
		stats.CurrentLoad = rlps.currentLoad.Load().(float64)
		stats.AdaptiveFactor = rlps.adaptiveFactor.Load().(float64)
	}

	// Aggregate token bucket stats
	if rlps.globalLimiter != nil {
		stats.GlobalAllowed = rlps.globalLimiter.allowed.Load()
		stats.GlobalDenied = rlps.globalLimiter.denied.Load()
	}

	rlps.topicLimitersMu.RLock()
	for _, limiter := range rlps.topicLimiters {
		stats.TopicAllowed += limiter.allowed.Load()
		stats.TopicDenied += limiter.denied.Load()
	}
	stats.TopicLimiters = len(rlps.topicLimiters)
	rlps.topicLimitersMu.RUnlock()

	rlps.subLimitersMu.RLock()
	for _, limiter := range rlps.subLimiters {
		stats.SubAllowed += limiter.allowed.Load()
		stats.SubDenied += limiter.denied.Load()
	}
	stats.SubLimiters = len(rlps.subLimiters)
	rlps.subLimitersMu.RUnlock()

	return stats
}

// RateLimitStats holds rate limiting metrics
type RateLimitStats struct {
	LimitExceeded  uint64
	LimitWaited    uint64
	AdaptiveAdj    uint64
	CurrentLoad    float64
	AdaptiveFactor float64

	GlobalAllowed uint64
	GlobalDenied  uint64

	TopicAllowed  uint64
	TopicDenied   uint64
	TopicLimiters int

	SubAllowed  uint64
	SubDenied   uint64
	SubLimiters int
}
