package messaging

import (
	"context"
	"time"

	"github.com/birdordev/plumego/x/resilience/ratelimit"
)

// RateLimiter is the rate limiter type used by [RateLimitedSMSProvider] and
// [RateLimitedEmailProvider]. It delegates to x/resilience/ratelimit.TokenBucket.
type RateLimiter = ratelimit.TokenBucket

// NewRateLimiter returns a RateLimiter that permits maxPerInterval calls per
// interval. It delegates to x/resilience/ratelimit.New.
func NewRateLimiter(maxPerInterval int, interval time.Duration) *RateLimiter {
	if maxPerInterval <= 0 {
		maxPerInterval = 100
	}
	if interval <= 0 {
		interval = time.Second
	}
	rate := float64(maxPerInterval) / interval.Seconds()
	return ratelimit.New(rate, int64(maxPerInterval))
}

// rateLimitedSend waits for a token then delegates to the inner send function.
func rateLimitedSend[M any, R any](ctx context.Context, limiter *RateLimiter, inner func(context.Context, M) (*R, error), msg M) (*R, error) {
	if err := limiter.Wait(ctx); err != nil {
		return nil, err
	}
	return inner(ctx, msg)
}

// RateLimitedSMSProvider wraps an SMSProvider with rate limiting.
type RateLimitedSMSProvider struct {
	inner   SMSProvider
	limiter *RateLimiter
}

// NewRateLimitedSMSProvider wraps provider with rate limiting (maxPerSecond calls/sec).
func NewRateLimitedSMSProvider(inner SMSProvider, maxPerSecond int) *RateLimitedSMSProvider {
	return &RateLimitedSMSProvider{
		inner:   inner,
		limiter: NewRateLimiter(maxPerSecond, time.Second),
	}
}

func (p *RateLimitedSMSProvider) Name() string { return p.inner.Name() }

func (p *RateLimitedSMSProvider) Send(ctx context.Context, msg SMSMessage) (*SMSResult, error) {
	return rateLimitedSend(ctx, p.limiter, p.inner.Send, msg)
}

// RateLimitedEmailProvider wraps an EmailProvider with rate limiting.
type RateLimitedEmailProvider struct {
	inner   EmailProvider
	limiter *RateLimiter
}

// NewRateLimitedEmailProvider wraps provider with rate limiting (maxPerSecond calls/sec).
func NewRateLimitedEmailProvider(inner EmailProvider, maxPerSecond int) *RateLimitedEmailProvider {
	return &RateLimitedEmailProvider{
		inner:   inner,
		limiter: NewRateLimiter(maxPerSecond, time.Second),
	}
}

func (p *RateLimitedEmailProvider) Name() string { return p.inner.Name() }

func (p *RateLimitedEmailProvider) Send(ctx context.Context, msg EmailMessage) (*EmailResult, error) {
	return rateLimitedSend(ctx, p.limiter, p.inner.Send, msg)
}
