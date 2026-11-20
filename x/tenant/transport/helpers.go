package transport

import (
	"net/http"
	"strconv"
	"time"
)

const (
	DefaultTenantHeader = "X-Tenant-ID"
	DefaultModelHeader  = "X-Model"
	DefaultToolHeader   = "X-Tool"
	DefaultTokensHeader = "X-Token-Count"

	CodeRequired      = "tenant_required"
	CodeInvalidID     = "tenant_invalid_id"
	CodeRateLimited   = "tenant_rate_limited"
	CodePolicyDenied  = "tenant_policy_denied"
	CodeQuotaExceeded = "tenant_quota_exceeded"
)

func HeaderOrDefault(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}

// SetRetryAfterHeader writes positive delays truncated to whole seconds.
func SetRetryAfterHeader(w http.ResponseWriter, retry time.Duration) {
	if retry <= 0 {
		return
	}
	w.Header().Set("Retry-After", strconv.FormatInt(int64(retry/time.Second), 10))
}

func SetRateLimitHeaders(w http.ResponseWriter, limit, remaining int64) {
	if limit > 0 {
		w.Header().Set("X-RateLimit-Limit", strconv.FormatInt(limit, 10))
	}
	w.Header().Set("X-RateLimit-Remaining", strconv.FormatInt(remaining, 10))
}

// SetQuotaHeaders sets X-Quota-Remaining-Requests and X-Quota-Remaining-Tokens headers.
// Values < 0 indicate unlimited and are omitted.
func SetQuotaHeaders(w http.ResponseWriter, remainingRequests, remainingTokens int64) {
	if remainingRequests >= 0 {
		w.Header().Set("X-Quota-Remaining-Requests", strconv.FormatInt(remainingRequests, 10))
	}
	if remainingTokens >= 0 {
		w.Header().Set("X-Quota-Remaining-Tokens", strconv.FormatInt(remainingTokens, 10))
	}
}
