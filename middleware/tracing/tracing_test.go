package tracing

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/birdordev/plumego/contract"
	internaltelemetry "github.com/birdordev/plumego/middleware/internal/telemetry"
)

type spanContextSpan struct {
	traceID    string
	spanID     string
	ended      bool
	panicOnEnd bool
}

func (s *spanContextSpan) End(status, bytes int, traceID string) {
	if s.panicOnEnd {
		panic("span end panic")
	}
	s.ended = true
}
func (s *spanContextSpan) TraceID() string { return s.traceID }
func (s *spanContextSpan) SpanID() string  { return s.spanID }

type spanContextTracer struct {
	span         *spanContextSpan
	panicOnStart bool
	panicOnEnd   bool
	skipContext  bool
}

func (t *spanContextTracer) Start(ctx context.Context, r *http.Request) (context.Context, TraceSpan) {
	if t.panicOnStart {
		panic("span start panic")
	}
	t.span = &spanContextSpan{
		traceID:    "1234567890abcdef1234567890abcdef",
		spanID:     "1234567890abcdef",
		panicOnEnd: t.panicOnEnd,
	}
	if !t.skipContext {
		ctx = contract.WithTraceContext(ctx, contract.TraceContext{
			TraceID: t.span.traceID,
			SpanID:  t.span.spanID,
		})
	}
	return ctx, t.span
}

func TestMiddlewareSetsTraceHeadersAndSpanContext(t *testing.T) {
	tracer := &spanContextTracer{}
	handler := Middleware(tracer)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tc, ok := contract.TraceContextFromContext(r.Context())
		if !ok || tc.TraceID != tracer.span.traceID || tc.SpanID != tracer.span.spanID {
			t.Fatalf("expected trace context from tracer span identity, got %+v", tc)
		}
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte("ok"))
	}))

	req := httptest.NewRequest(http.MethodGet, "/trace", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Header().Get(contract.RequestIDHeader) == "" {
		t.Fatalf("expected request id header to be set")
	}
	if rec.Header().Get(internaltelemetry.SpanIDHeader) != tracer.span.spanID {
		t.Fatalf("expected span id header to be set")
	}
	if tracer.span == nil || !tracer.span.ended {
		t.Fatalf("expected tracer span to be ended")
	}
}

func TestMiddlewareDerivesTraceContextFromReturnedSpan(t *testing.T) {
	tracer := &spanContextTracer{skipContext: true}
	handler := Middleware(tracer)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tc, ok := contract.TraceContextFromContext(r.Context())
		if !ok || tc.TraceID != tracer.span.traceID || tc.SpanID != tracer.span.spanID {
			t.Fatalf("expected trace context to be derived from returned span, got %+v", tc)
		}
		if !tc.Valid() {
			t.Fatalf("expected derived trace context to be valid, got %+v", tc)
		}
		w.WriteHeader(http.StatusAccepted)
	}))

	req := httptest.NewRequest(http.MethodGet, "/trace", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if got := rec.Header().Get(internaltelemetry.SpanIDHeader); got != tracer.span.spanID {
		t.Fatalf("span id header = %q, want %q", got, tracer.span.spanID)
	}
}

func TestMiddlewareEndsSpanOnPanic(t *testing.T) {
	tracer := &spanContextTracer{}
	handler := Middleware(tracer)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte("partial"))
		panic("boom")
	}))

	req := httptest.NewRequest(http.MethodGet, "/panic", nil)
	rec := httptest.NewRecorder()
	panicked := false
	func() {
		defer func() {
			if recover() != nil {
				panicked = true
			}
		}()
		handler.ServeHTTP(rec, req)
	}()

	if !panicked {
		t.Fatal("expected panic to propagate")
	}
	if tracer.span == nil || !tracer.span.ended {
		t.Fatalf("expected tracer span to end during panic unwinding")
	}
}

func TestMiddlewarePreservesDownstreamPanicWhenSpanEndPanics(t *testing.T) {
	tracer := &spanContextTracer{panicOnEnd: true}
	handler := Middleware(tracer)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		panic("downstream panic")
	}))

	req := httptest.NewRequest(http.MethodGet, "/panic", nil)
	rec := httptest.NewRecorder()
	defer func() {
		if rec := recover(); rec != "downstream panic" {
			t.Fatalf("panic = %v, want downstream panic", rec)
		}
	}()
	handler.ServeHTTP(rec, req)
}

func TestMiddlewareContinuesWhenTracerStartPanics(t *testing.T) {
	tracer := &spanContextTracer{panicOnStart: true}
	called := false
	handler := Middleware(tracer)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		if tc, ok := contract.TraceContextFromContext(r.Context()); ok {
			t.Fatalf("trace context = %+v, want nil after start panic", tc)
		}
		w.WriteHeader(http.StatusAccepted)
	}))

	req := httptest.NewRequest(http.MethodGet, "/trace", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if !called {
		t.Fatal("expected downstream handler to run")
	}
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusAccepted)
	}
	if got := rec.Header().Get(internaltelemetry.SpanIDHeader); got != "" {
		t.Fatalf("span id header = %q, want empty", got)
	}
}

// TestMiddlewareNilTracerPassthrough exercises the nil-tracer branch in
// Middleware that returns the next handler directly without wrapping.
func TestMiddlewareNilTracerPassthrough(t *testing.T) {
	called := false
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusNoContent)
	})
	handler := Middleware(nil)(inner)

	req := httptest.NewRequest(http.MethodGet, "/passthrough", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if !called {
		t.Fatal("expected inner handler to be called when tracer is nil")
	}
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusNoContent)
	}
}
