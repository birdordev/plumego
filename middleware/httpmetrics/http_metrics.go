package httpmetrics

import (
	"net/http"

	"github.com/spcent/plumego/metrics"
	"github.com/spcent/plumego/middleware"
	internaltelemetry "github.com/spcent/plumego/middleware/internal/telemetry"
)

// Observer is the metrics sink for HTTP request observations. It is an alias
// for [metrics.HTTPObserver]; implement it with a collector to record request
// metrics, or pass nil to [Middleware] for a no-op pass-through.
type Observer = metrics.HTTPObserver

// Middleware records HTTP request metrics using the provided collector.
//
// If collector is nil the middleware is a no-op pass-through. Otherwise it
// wraps the response writer, measures duration, status, and bytes, and
// forwards the observation to the collector after the handler completes.
func Middleware(collector Observer) middleware.Middleware {
	return func(next http.Handler) http.Handler {
		if collector == nil {
			return next
		}

		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			internaltelemetry.WithObserver(next, w, r, func(metricsData internaltelemetry.RequestMetrics, _ *internaltelemetry.ResponseRecorder, r *http.Request) {
				collector.ObserveHTTP(r.Context(), metricsData.Method, metricsData.ObservedPath(), metricsData.Status, metricsData.Bytes, metricsData.Duration)
			})
		})
	}
}
