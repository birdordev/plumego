package webhook

import (
	"math/rand"
	"time"
)

// RetryDecision represents the result of a retry evaluation.
type RetryDecision struct {
	Retry bool
	Next  time.Time
	Why   string
}

// ShouldRetry determines if a webhook delivery should be retried.
// Returns true if the delivery should be retried based on status code, error, and attempt count.
func ShouldRetry(status int, err error, attempt int, maxRetries int, retryOn429 bool) bool {
	if attempt >= maxRetries {
		return false
	}

	// Network errors or timeouts should be retried
	if err != nil {
		return true
	}

	// Server errors (5xx) should be retried
	if status >= 500 && status <= 599 {
		return true
	}

	// 429 Too Many Requests (optional based on config)
	if retryOn429 && status == 429 {
		return true
	}

	// 408 Request Timeout could be retried
	if status == 408 {
		return true
	}

	// 503 Service Unavailable should be retried
	if status == 503 {
		return true
	}

	return false
}

// NextBackoff calculates the next retry time using exponential backoff with jitter.
// Formula: base * 2^(attempt-1) * jitter[0.5, 1.5)
func NextBackoff(now time.Time, attempt int, base, max time.Duration) time.Time {
	if attempt < 1 {
		attempt = 1
	}

	// Calculate exponential backoff
	d := base
	for i := 1; i < attempt; i++ {
		if d >= max/2 {
			d = max
			break
		}
		d *= 2
	}

	// Clamp to max
	if d > max {
		d = max
	}

	// Add jitter [0.5, 1.5)
	jitter := 0.5 + rand.Float64()
	wait := time.Duration(float64(d) * jitter)

	return now.Add(wait)
}

// GetRetryReason returns a human-readable reason for retrying.
// A network error takes precedence over the HTTP status.
func GetRetryReason(status int, err error, retryOn429 bool) string {
	if err != nil {
		return "network_error"
	}
	if status == 503 {
		return "service_unavailable"
	}
	if status >= 500 && status <= 599 {
		return "server_error"
	}
	if retryOn429 && status == 429 {
		return "rate_limited"
	}
	if status == 408 {
		return "timeout"
	}
	return "unknown"
}
