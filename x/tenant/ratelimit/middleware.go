// Package ratelimit provides tenant-scoped HTTP rate limiting middleware.
//
// This is the tenant-aware layer: it applies per-tenant limits using
// x/resilience/ratelimit.TokenBucket as the underlying primitive. For
// general HTTP abuse protection without tenant context use middleware/abuseguard.
package ratelimit

import (
	"net/http"
	"time"

	"github.com/birdordev/plumego/contract"
	"github.com/birdordev/plumego/middleware"
	tenantcore "github.com/birdordev/plumego/x/tenant/core"
	tenanttransport "github.com/birdordev/plumego/x/tenant/transport"
)

// Options configures rate limit enforcement.
type Options struct {
	Limiter    tenantcore.RateLimiter
	Estimator  func(*http.Request) int64
	Hooks      tenantcore.Hooks
	OnRejected func(http.ResponseWriter, *http.Request, tenantcore.RateLimitResult)
}

// Middleware enforces per-tenant rate limits.
func Middleware(options Options) middleware.Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if options.Limiter == nil {
				next.ServeHTTP(w, r)
				return
			}

			tenantID := tenantcore.TenantIDFromContext(r.Context())
			if tenantID == "" {
				next.ServeHTTP(w, r)
				return
			}

			tokens := int64(1)
			if options.Estimator != nil {
				if t := options.Estimator(r); t > 0 {
					tokens = t
				}
			}

			result, err := options.Limiter.Allow(r.Context(), tenantID, tenantcore.RateLimitRequest{
				Tokens: tokens,
				Now:    time.Now().UTC(),
			})
			allowed := err == nil && result.Allowed

			status := http.StatusOK
			if !allowed {
				status = http.StatusTooManyRequests
			}

			options.Hooks.RateLimit(r.Context(), tenantcore.RateLimitDecision{
				TenantID:   tenantID,
				Allowed:    allowed,
				Tokens:     tokens,
				Remaining:  result.Remaining,
				Limit:      result.Limit,
				Burst:      result.Burst,
				RetryAfter: result.RetryAfter,
				Status:     status,
				Err:        err,
			})

			if allowed {
				next.ServeHTTP(w, r)
				return
			}

			tenanttransport.SetRetryAfterHeader(w, result.RetryAfter)
			tenanttransport.SetRateLimitHeaders(w, result.Limit, result.Remaining)

			if options.OnRejected != nil {
				options.OnRejected(w, r, result)
				return
			}

			_ = contract.WriteError(w, r, contract.NewErrorBuilder().
				Type(contract.TypeRateLimited).
				Code(tenanttransport.CodeRateLimited).
				Message("tenant rate limit exceeded").
				Build())
		})
	}
}
