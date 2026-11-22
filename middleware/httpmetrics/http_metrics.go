package httpmetrics

import (
	"net/http"

	"github.com/birdordev/plumego/metrics"
	"github.com/birdordev/plumego/middleware"
	internaltelemetry "github.com/birdordev/plumego/middleware/internal/telemetry"
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
	return observe(collector, false)
}

// MiddlewareWithRouteLabels uses bounded fallback labels for requests without a
// known route. Matched paths use their route pattern when available.
func MiddlewareWithRouteLabels(collector Observer) middleware.Middleware {
	return observe(collector, true)
}

func observe(collector Observer, routeLabels bool) middleware.Middleware {
	return func(next http.Handler) http.Handler {
		if collector == nil {
			return next
		}

		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			internaltelemetry.WithObserver(next, w, r, func(metricsData internaltelemetry.RequestMetrics, _ *internaltelemetry.ResponseRecorder, r *http.Request) {
				label := metricsData.ObservedPath()
				if routeLabels && metricsData.Route == "" {
					label = r.Pattern
					if label == "" {
						switch metricsData.Status {
						case http.StatusNotFound, http.StatusMethodNotAllowed:
							label = "unmatched"
						default:
							label = "unclassified"
						}
					}
				}
				collector.ObserveHTTP(r.Context(), metricsData.Method, label, metricsData.Status, metricsData.Bytes, metricsData.Duration)
			})
		})
	}
}
