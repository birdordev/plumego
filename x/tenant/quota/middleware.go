package quota

import (
	"net/http"
	"strconv"
	"time"

	"github.com/birdordev/plumego/contract"
	mw "github.com/birdordev/plumego/middleware"
	tenantcore "github.com/birdordev/plumego/x/tenant/core"
	tenanttransport "github.com/birdordev/plumego/x/tenant/transport"
)

// Options configures quota enforcement.
type Options struct {
	Manager      tenantcore.QuotaManager
	TokensHeader string
	Estimator    func(*http.Request) int
	Hooks        tenantcore.Hooks
	OnRejected   func(http.ResponseWriter, *http.Request, tenantcore.QuotaResult)
}

// Middleware enforces tenant quota limits.
func Middleware(options Options) mw.Middleware {
	tokensHeader := tenanttransport.HeaderOrDefault(options.TokensHeader, tenanttransport.DefaultTokensHeader)

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if options.Manager == nil {
				next.ServeHTTP(w, r)
				return
			}

			tenantID := tenantcore.TenantIDFromContext(r.Context())
			var tokens int64
			if options.Estimator != nil {
				tokens = int64(options.Estimator(r))
			} else if header := r.Header.Get(tokensHeader); header != "" {
				if value, err := strconv.ParseInt(header, 10, 64); err == nil {
					tokens = value
				}
			}

			result, err := options.Manager.Allow(r.Context(), tenantID, tenantcore.QuotaRequest{
				Requests: 1,
				Tokens:   tokens,
				Now:      time.Now().UTC(),
			})
			allowed := err == nil && result.Allowed

			status := http.StatusOK
			if !allowed {
				status = http.StatusTooManyRequests
			}

			options.Hooks.Quota(r.Context(), tenantcore.QuotaDecision{
				TenantID:          tenantID,
				Allowed:           allowed,
				Tokens:            tokens,
				Requests:          1,
				RemainingTokens:   result.RemainingTokens,
				RemainingRequests: result.RemainingRequests,
				RetryAfter:        result.RetryAfter,
				Status:            status,
				Err:               err,
			})

			tenanttransport.SetQuotaHeaders(w, result.RemainingRequests, result.RemainingTokens)

			if allowed {
				next.ServeHTTP(w, r)
				return
			}

			tenanttransport.SetRetryAfterHeader(w, result.RetryAfter)

			if options.OnRejected != nil {
				options.OnRejected(w, r, result)
				return
			}

			_ = contract.WriteError(w, r, contract.NewErrorBuilder().
				Type(contract.TypeRateLimited).
				Code(tenanttransport.CodeQuotaExceeded).
				Message("tenant quota exceeded").
				Build())
		})
	}
}
