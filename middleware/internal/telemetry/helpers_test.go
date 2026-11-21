package telemetry

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/birdordev/plumego/contract"
)

type stubSpan struct {
	ended     bool
	status    int
	bytes     int
	requestID string
	traceID   string
	spanID    string
}

func (s *stubSpan) End(status, bytes int, requestID string) {
	s.ended = true
	s.status = status
	s.bytes = bytes
	s.requestID = requestID
}

func (s *stubSpan) TraceID() string { return s.traceID }
func (s *stubSpan) SpanID() string  { return s.spanID }

type stubTracer struct {
	span  *stubSpan
	panic bool
}

func (t *stubTracer) Start(ctx context.Context, r *http.Request) (context.Context, TraceSpan) {
	if t.panic {
		panic("trace start panic")
	}
	t.span = &stubSpan{
		traceID: "1234567890abcdef1234567890abcdef",
		spanID:  "1234567890abcdef",
	}
	ctx = contract.WithTraceContext(ctx, contract.TraceContext{
		TraceID: t.span.traceID,
		SpanID:  t.span.spanID,
	})
	return ctx, t.span
}

func TestPrepareRequestAddsFallbackRequestID(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/fallback", nil)
	rec := httptest.NewRecorder()

	prepared := PrepareRequest(rec, req)

	if prepared.RequestID == "" {
		t.Fatal("expected fallback request id to be generated")
	}
	if got := rec.Header().Get(contract.RequestIDHeader); got == "" {
		t.Fatalf("expected %s header to be set", contract.RequestIDHeader)
	}
	if got := contract.RequestIDFromContext(prepared.Request.Context()); got != prepared.RequestID {
		t.Fatalf("expected request context id %q, got %q", prepared.RequestID, got)
	}
}

func TestBeginTraceAttachesSpanHeaderAndContext(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/trace", nil)
	rec := httptest.NewRecorder()
	prepared := PrepareRequest(rec, req)
	tracer := &stubTracer{}

	r, span := BeginTrace(rec, prepared, tracer.Start)

	if span == nil {
		t.Fatal("expected span to be returned")
	}
	if got := rec.Header().Get(SpanIDHeader); got != tracer.span.spanID {
		t.Fatalf("expected span header %q, got %q", tracer.span.spanID, got)
	}
	tc, ok := contract.TraceContextFromContext(r.Context())
	if !ok || tc.TraceID != tracer.span.traceID || tc.SpanID != tracer.span.spanID {
		t.Fatalf("expected trace context from span identity, got %+v", tc)
	}
}

func TestBeginTraceDerivesContextFromReturnedSpan(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/trace", nil)
	rec := httptest.NewRecorder()
	prepared := PrepareRequest(rec, req)
	span := &stubSpan{
		traceID: "1234567890abcdef1234567890abcdef",
		spanID:  "1234567890abcdef",
	}

	r, gotSpan := BeginTrace(rec, prepared, func(ctx context.Context, r *http.Request) (context.Context, TraceSpan) {
		return ctx, span
	})

	if gotSpan != span {
		t.Fatalf("span = %v, want original span", gotSpan)
	}
	tc, ok := contract.TraceContextFromContext(r.Context())
	if !ok || tc.TraceID != span.traceID || tc.SpanID != span.spanID {
		t.Fatalf("expected trace context to be derived from span, got %+v", tc)
	}
	if !tc.Valid() {
		t.Fatalf("expected derived trace context to be valid, got %+v", tc)
	}
	if got := rec.Header().Get(SpanIDHeader); got != span.spanID {
		t.Fatalf("span header = %q, want %q", got, span.spanID)
	}
}

func TestBeginTraceRecoversStartPanic(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/trace", nil)
	rec := httptest.NewRecorder()
	prepared := PrepareRequest(rec, req)
	tracer := &stubTracer{panic: true}

	r, span := BeginTrace(rec, prepared, tracer.Start)

	if r != prepared.Request {
		t.Fatal("expected original prepared request after tracer start panic")
	}
	if span != nil {
		t.Fatalf("span = %v, want nil", span)
	}
	if got := rec.Header().Get(SpanIDHeader); got != "" {
		t.Fatalf("span header = %q, want empty", got)
	}
}

func TestPreparedRequestCompleteAndEndTrace(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/users/42", nil)
	req = req.WithContext(contract.WithRequestContext(req.Context(), contract.RequestContext{
		RoutePattern: "/users/:id",
	}))
	rec := httptest.NewRecorder()
	prepared := PrepareRequest(rec, req)
	prepared.Recorder.WriteHeader(http.StatusCreated)
	_, _ = prepared.Recorder.Write([]byte("ok"))

	metrics := prepared.Complete(prepared.Request)
	if metrics.ObservedPath() != "/users/:id" {
		t.Fatalf("expected observed path %q, got %q", "/users/:id", metrics.ObservedPath())
	}

	span := &stubSpan{}
	EndTrace(span, metrics)
	if !span.ended {
		t.Fatal("expected span to be ended")
	}
	if span.status != http.StatusCreated || span.bytes != len("ok") {
		t.Fatalf("unexpected span end payload: status=%d bytes=%d", span.status, span.bytes)
	}
	if span.requestID != metrics.RequestID {
		t.Fatalf("expected request id %q, got %q", metrics.RequestID, span.requestID)
	}
}

func TestWithObserverSharesRouteBetweenMetricsAndRequest(t *testing.T) {
	const path = "/users/42"
	tests := []struct {
		name      string
		inherited string
		published string
		want      string
	}{
		{"no route", "", "", ""},
		{"context route", "/users/:id", "", "/users/:id"},
		{"published route takes precedence", "/old/:id", "/users/:id", "/users/:id"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, path, nil)
			const requestID = "fixture-id"
			req = req.WithContext(contract.WithRequestID(req.Context(), requestID))
			if tt.inherited != "" {
				req = req.WithContext(contract.WithRequestContext(req.Context(), contract.RequestContext{
					RoutePattern: tt.inherited,
				}))
			}
			next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if tt.published != "" {
					w.(*ResponseRecorder).SetRouteContext(contract.WithRequestContext(r.Context(), contract.RequestContext{
						RoutePattern: tt.published,
					}))
				}
				w.WriteHeader(http.StatusCreated)
				_, _ = w.Write([]byte("ok"))
			})

			calls := 0
			WithObserver(next, httptest.NewRecorder(), req, func(metrics RequestMetrics, _ *ResponseRecorder, observed *http.Request) {
				calls++
				route := contract.RequestContextFromContext(observed.Context()).RoutePattern
				if route != tt.want || metrics.Route != tt.want {
					t.Errorf("request route = %q, metrics route = %q, want %q", route, metrics.Route, tt.want)
				}
				wantPath := tt.want
				if wantPath == "" {
					wantPath = path
				}
				if got := metrics.ObservedPath(); got != wantPath {
					t.Errorf("observed path = %q, want %q", got, wantPath)
				}
				if metrics.RequestID != requestID || contract.RequestIDFromContext(observed.Context()) != requestID ||
					metrics.Status != http.StatusCreated || metrics.Bytes != len("ok") {
					t.Errorf("unexpected metrics or request ID: %+v", metrics)
				}
			})
			if calls != 1 {
				t.Fatalf("observe called %d times, want 1", calls)
			}
		})
	}
}

func TestBeginTracePublishesOnlyValidTraceIdentity(t *testing.T) {
	for _, useContext := range []bool{false, true} {
		w := httptest.NewRecorder()
		request := PrepareRequest(w, httptest.NewRequest(http.MethodGet, "/", nil))
		span := &stubSpan{traceID: "placeholder", spanID: "placeholder"}
		req, got := BeginTrace(w, request, func(ctx context.Context, _ *http.Request) (context.Context, TraceSpan) {
			if useContext {
				ctx = contract.WithTraceContext(ctx, contract.TraceContext{
					TraceID: "1234567890abcdef1234567890abcdef", SpanID: "1234567890abcdef",
				})
			}
			return ctx, span
		})
		want := ""
		if useContext {
			want = "1234567890abcdef"
		}
		trace, _ := contract.TraceContextFromContext(req.Context())
		if w.Header().Get(SpanIDHeader) != want || trace.Valid() != useContext {
			t.Fatalf("header=%q trace=%+v", w.Header().Get(SpanIDHeader), trace)
		}
		EndTrace(got, RequestMetrics{Status: http.StatusCreated})
		if !span.ended || span.status != http.StatusCreated {
			t.Fatal("span did not finish")
		}
	}
}
