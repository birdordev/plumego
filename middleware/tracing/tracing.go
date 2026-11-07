// Package tracing provides a transport-layer tracing middleware. It defines
// the [Tracer] and [TraceSpan] interfaces that callers must implement; it does
// not own the concrete tracing infrastructure.
//
// Concrete implementations (OpenTelemetry adapters, span collectors, samplers)
// belong in x/observability/tracer. The interface is defined here so that
// stable middleware can remain decoupled from extension packages: stable roots
// must not import x/*.
package tracing

import (
	"context"
	"net/http"

	"github.com/spcent/plumego/middleware"
	internaltelemetry "github.com/spcent/plumego/middleware/internal/telemetry"
)

// TraceSpan is the span contract returned by [Tracer.Start]. It is an alias
// for the internal telemetry span type so stable middleware stays decoupled
// from the concrete tracing infrastructure.
type TraceSpan = internaltelemetry.TraceSpan

// Tracer starts a span for an HTTP request and returns the derived context and
// span. Implementations own span lifecycle and sampling; see [Middleware].
type Tracer interface {
	Start(ctx context.Context, r *http.Request) (context.Context, TraceSpan)
}

// Middleware creates a tracing middleware that starts a span for each request
// using the provided tracer.
//
// If tracer is nil the middleware is a no-op pass-through. The middleware
// measures request timing and delegates span lifecycle to the tracer
// implementation. Concrete tracers (OpenTelemetry, custom, etc.) live in
// x/observability/tracer; this package only defines the interface.
func Middleware(tracer Tracer) middleware.Middleware {
	return func(next http.Handler) http.Handler {
		if tracer == nil {
			return next
		}

		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			prepared := internaltelemetry.PrepareRequest(w, r)
			r, span := internaltelemetry.BeginTrace(w, prepared, func(ctx context.Context, r *http.Request) (context.Context, internaltelemetry.TraceSpan) {
				return tracer.Start(ctx, r)
			})
			recorder := prepared.Recorder
			defer internaltelemetry.FinishPreservingPanic(func() {
				internaltelemetry.EndTrace(span, prepared.Complete(r))
			})

			next.ServeHTTP(recorder, r)
		})
	}
}
