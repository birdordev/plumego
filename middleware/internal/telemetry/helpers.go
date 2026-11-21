package telemetry

import (
	"context"
	"net/http"
	"time"

	"github.com/birdordev/plumego/contract"
	internaltransport "github.com/birdordev/plumego/internal/httputil"
	"github.com/birdordev/plumego/middleware/requestid"
)

// RequestMetrics captures common observability attributes for a request.
type RequestMetrics struct {
	Method    string
	Path      string
	Route     string
	Status    int
	Bytes     int
	Duration  time.Duration
	RequestID string
	UserAgent string
}

// PreparedRequest captures the shared request state stable observability
// middleware needs before delegating to the next handler.
type PreparedRequest struct {
	Request   *http.Request
	Recorder  *ResponseRecorder
	RequestID string
	StartedAt time.Time
}

// PrepareRequest establishes the canonical observability fallback path for
// stable middleware. When requestid.Middleware() is not present, observability
// middleware still stamps a request ID so logging, metrics, and tracing share
// the same correlation surface.
func PrepareRequest(w http.ResponseWriter, r *http.Request) PreparedRequest {
	requestID := requestid.EnsureRequestID(r, nil)
	r = requestid.AttachRequestID(w, r, requestID, false)
	return PreparedRequest{
		Request:   r,
		Recorder:  internaltransport.NewObservationRecorder(w),
		RequestID: requestID,
		StartedAt: time.Now(),
	}
}

// Complete derives request metrics from the prepared request lifecycle state.
func (p PreparedRequest) Complete(r *http.Request) RequestMetrics {
	metrics, _ := p.complete(r)
	return metrics
}

func (p PreparedRequest) complete(r *http.Request) (RequestMetrics, *http.Request) {
	observed := p.observedRequest(r)
	return BuildRequestMetrics(observed, p.Recorder, p.StartedAt, p.RequestID), observed
}

type TraceSpan interface {
	TraceID() string
	SpanID() string
	End(status, bytes int, requestID string)
}

type TraceStarter func(ctx context.Context, r *http.Request) (context.Context, TraceSpan)

// BeginTrace applies the shared tracing start flow used by stable observability
// middleware. When tracer is nil the request is returned unchanged.
func BeginTrace(w http.ResponseWriter, prepared PreparedRequest, start TraceStarter) (*http.Request, TraceSpan) {
	r := prepared.Request
	if start == nil {
		return r, nil
	}

	var (
		ctx      context.Context
		span     TraceSpan
		panicked bool
		spanID   string
		traceID  string
	)
	func() {
		defer func() {
			if recover() != nil {
				panicked = true
			}
		}()
		ctx, span = start(r.Context(), r)
		if ctx == nil {
			ctx = r.Context()
		}
		if span != nil {
			traceID = span.TraceID()
			spanID = span.SpanID()
		}
	}()
	if panicked {
		// Keep ownership of a span created before metadata failed so the
		// normal best-effort finalizer still ends it exactly once.
		return r, span
	}
	trace, _ := contract.TraceContextFromContext(ctx)
	if !trace.Valid() {
		candidate := contract.TraceContext{TraceID: traceID, SpanID: spanID}
		if candidate.Valid() {
			trace.TraceID, trace.SpanID = candidate.TraceID, candidate.SpanID
			ctx = contract.WithTraceContext(ctx, trace)
		}
	}
	r = r.WithContext(ctx)
	if trace.Valid() && w != nil {
		w.Header().Set(SpanIDHeader, trace.SpanID)
	}
	return r, span
}

// EndTrace applies the canonical trace completion path for stable
// observability middleware.
func EndTrace(span TraceSpan, metrics RequestMetrics) {
	if span == nil {
		return
	}
	span.End(metrics.Status, metrics.Bytes, metrics.RequestID)
}

// FinishPreservingPanic runs observability finalizers without allowing them to
// replace a panic raised by downstream application code.
func FinishPreservingPanic(finalize func()) {
	recovered := recover()
	RunSafeFinalizer(finalize)
	if recovered != nil {
		panic(recovered)
	}
}

// RunSafeFinalizer runs a best-effort observability finalizer and recovers any
// panic it raises. Observability failures must not change transport behavior.
func RunSafeFinalizer(finalize func()) {
	if finalize == nil {
		return
	}
	defer func() {
		_ = recover()
	}()
	finalize()
}

func BuildRequestMetrics(r *http.Request, recorder *ResponseRecorder, started time.Time, requestID string) RequestMetrics {
	rc := contract.RequestContextFromContext(r.Context())
	metricsData := RequestMetrics{
		Method:    r.Method,
		Path:      r.URL.Path,
		Status:    recorder.StatusCode(),
		Bytes:     recorder.BytesWritten(),
		Duration:  time.Since(started),
		RequestID: requestID,
		UserAgent: r.UserAgent(),
	}
	if rc.RoutePattern != "" {
		metricsData.Route = rc.RoutePattern
	}
	return metricsData
}

// ObservedPath returns the canonical path label for transport metrics.
func (m RequestMetrics) ObservedPath() string {
	if m.Route != "" {
		return m.Route
	}
	return m.Path
}

// observedRequest combines downstream route identity with the observer's request.
func (p PreparedRequest) observedRequest(r *http.Request) *http.Request {
	if rc := p.Recorder.RouteContext(); rc.RoutePattern != "" {
		return r.WithContext(contract.WithRequestContext(r.Context(), rc))
	}
	return r
}

// WithObserver wraps the next handler with the canonical PrepareRequest lifecycle
// and calls observe after the handler completes. It preserves panics raised by
// downstream code so they bubble correctly through the middleware chain.
//
// The observe callback receives:
//   - metrics: the completed request metrics derived from r
//   - recorder: the response recorder wrapping the original writer
//   - r: the modified request (post-handler, for context reads like route/span)
func WithObserver(next http.Handler, w http.ResponseWriter, r *http.Request, observe func(metrics RequestMetrics, recorder *ResponseRecorder, r *http.Request)) {
	prepared := PrepareRequest(w, r)
	recorder := prepared.Recorder
	r = prepared.Request

	defer FinishPreservingPanic(func() {
		metrics, observed := prepared.complete(r)
		observe(metrics, recorder, observed)
	})

	next.ServeHTTP(recorder, r)
}

type ResponseRecorder = internaltransport.ResponseRecorder
