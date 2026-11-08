package concurrencylimit

import (
	"net/http"
	"time"

	"github.com/birdordev/plumego/contract"
	internaltransport "github.com/birdordev/plumego/internal/httputil"
	mw "github.com/birdordev/plumego/middleware"
)

const defaultQueueTimeout = 100 * time.Millisecond

// Config controls concurrency limiting behavior.
type Config struct {
	// MaxConcurrent is the maximum number of requests processed at once. Values
	// less than or equal to zero disable the middleware.
	MaxConcurrent int

	// QueueDepth is the number of additional requests allowed to wait for a
	// worker. Negative values are treated as zero.
	QueueDepth int

	// QueueTimeout is the maximum time a queued request can wait. A non-positive
	// value uses the package default.
	QueueTimeout time.Duration
}

// DefaultConfig returns a concurrency limit config with package defaults.
func DefaultConfig(maxConcurrent int) Config {
	return Config{
		MaxConcurrent: maxConcurrent,
		QueueTimeout:  defaultQueueTimeout,
	}
}

// Middleware restricts how many requests can be processed concurrently using a
// named configuration object.
func Middleware(config Config) mw.Middleware {
	if config.MaxConcurrent <= 0 {
		return func(next http.Handler) http.Handler { return next }
	}

	if config.QueueDepth < 0 {
		config.QueueDepth = 0
	}
	if config.QueueTimeout <= 0 {
		config.QueueTimeout = defaultQueueTimeout
	}

	sem := make(chan struct{}, config.MaxConcurrent)
	queue := make(chan struct{}, config.MaxConcurrent+config.QueueDepth)

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			select {
			case <-r.Context().Done():
				return
			default:
			}

			select {
			case queue <- struct{}{}:
				defer func() { <-queue }()
			case <-r.Context().Done():
				return
			default:
				if r.Context().Err() != nil {
					return
				}
				internaltransport.WriteTransportError(w, r, http.StatusServiceUnavailable, contract.CodeServerBusy, "server is throttling concurrent requests", nil)
				return
			}

			timer := time.NewTimer(config.QueueTimeout)
			defer timer.Stop()

			select {
			case sem <- struct{}{}:
				defer func() { <-sem }()
			case <-r.Context().Done():
				return
			case <-timer.C:
				if r.Context().Err() != nil {
					return
				}
				internaltransport.WriteTransportError(w, r, http.StatusServiceUnavailable, contract.CodeServerQueueTimeout, "request timed out waiting for an available worker", map[string]any{
					"queue_occupancy": len(queue),
					"queue_capacity":  cap(queue),
				})
				return
			}

			if r.Context().Err() != nil {
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
