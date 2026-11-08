package accesslog

import (
	"bufio"
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	contract "github.com/birdordev/plumego/contract"
	"github.com/birdordev/plumego/log"
	"github.com/birdordev/plumego/middleware"
	mwtracing "github.com/birdordev/plumego/middleware/tracing"
)

type logEntry struct {
	msg    string
	fields log.Fields
}

type stubLogger struct {
	baseFields    log.Fields
	entries       *[]logEntry
	mu            *sync.Mutex
	panicOnRecord bool
}

func newStubLogger() *stubLogger {
	entries := make([]logEntry, 0)
	return &stubLogger{baseFields: log.Fields{}, entries: &entries, mu: &sync.Mutex{}}
}

func (l *stubLogger) WithFields(fields log.Fields) log.StructuredLogger {
	merged := make(log.Fields, len(l.baseFields)+len(fields))
	for k, v := range l.baseFields {
		merged[k] = v
	}
	for k, v := range fields {
		merged[k] = v
	}
	return &stubLogger{baseFields: merged, entries: l.entries, mu: l.mu, panicOnRecord: l.panicOnRecord}
}
func (l *stubLogger) With(key string, value any) log.StructuredLogger {
	return l.WithFields(log.Fields{key: value})
}
func (l *stubLogger) Debug(msg string, fields ...log.Fields) { l.record(msg, first(fields)) }
func (l *stubLogger) Info(msg string, fields ...log.Fields)  { l.record(msg, first(fields)) }
func (l *stubLogger) Warn(msg string, fields ...log.Fields)  { l.record(msg, first(fields)) }
func (l *stubLogger) Error(msg string, fields ...log.Fields) { l.record(msg, first(fields)) }
func (l *stubLogger) DebugCtx(ctx context.Context, msg string, fields ...log.Fields) {
	l.record(msg, first(fields))
}
func (l *stubLogger) InfoCtx(ctx context.Context, msg string, fields ...log.Fields) {
	l.record(msg, first(fields))
}
func (l *stubLogger) WarnCtx(ctx context.Context, msg string, fields ...log.Fields) {
	l.record(msg, first(fields))
}
func (l *stubLogger) ErrorCtx(ctx context.Context, msg string, fields ...log.Fields) {
	l.record(msg, first(fields))
}
func (l *stubLogger) Fatal(msg string, fields ...log.Fields) { l.record(msg, first(fields)) }
func (l *stubLogger) FatalCtx(ctx context.Context, msg string, fields ...log.Fields) {
	l.record(msg, first(fields))
}

func first(fields []log.Fields) log.Fields {
	if len(fields) > 0 {
		return fields[0]
	}
	return nil
}

func (l *stubLogger) record(msg string, fields log.Fields) {
	if l.panicOnRecord {
		panic("logger panic")
	}
	merged := make(log.Fields, len(l.baseFields)+len(fields))
	for k, v := range l.baseFields {
		merged[k] = v
	}
	for k, v := range fields {
		merged[k] = v
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	*l.entries = append(*l.entries, logEntry{msg: msg, fields: merged})
}

type spanContextSpan struct {
	traceID string
	spanID  string
	ended   bool
}

func (s *spanContextSpan) End(status, bytes int, traceID string) { s.ended = true }
func (s *spanContextSpan) TraceID() string                       { return s.traceID }
func (s *spanContextSpan) SpanID() string                        { return s.spanID }

type spanContextTracer struct{ span *spanContextSpan }

func (t *spanContextTracer) Start(ctx context.Context, r *http.Request) (context.Context, mwtracing.TraceSpan) {
	t.span = &spanContextSpan{
		traceID: "1234567890abcdef1234567890abcdef",
		spanID:  "1234567890abcdef",
	}
	return ctx, t.span
}

func mustAccesslog(tb testing.TB, logger log.StructuredLogger) middleware.Middleware {
	tb.Helper()
	mw, err := Middleware(Config{Logger: logger})
	if err != nil {
		tb.Fatalf("Middleware returned error: %v", err)
	}
	return mw
}

func TestMiddlewareAddsStructuredFields(t *testing.T) {
	logger := newStubLogger()

	mw := mustAccesslog(t, logger)
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if contract.RequestIDFromContext(r.Context()) == "" {
			t.Fatalf("request id should be present in context")
		}
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte("ok"))
	})

	req := httptest.NewRequest(http.MethodPost, "/structured", nil)
	req.Header.Set(contract.RequestIDHeader, "trace-123")
	rc := contract.RequestContext{RoutePattern: "/structured/:id", RouteName: "structured"}
	req = req.WithContext(contract.WithRequestContext(req.Context(), rc))
	rec := httptest.NewRecorder()
	mw(handler).ServeHTTP(rec, req)

	if len(*logger.entries) != 1 {
		t.Fatalf("expected one log entry, got %d", len(*logger.entries))
	}
	entry := (*logger.entries)[0]
	if entry.fields["request_id"] != "trace-123" {
		t.Fatalf("expected request id field")
	}
}

func TestMiddlewareCapturesSpanIDFromTracing(t *testing.T) {
	logger := newStubLogger()
	tracer := &spanContextTracer{}
	handler := mustAccesslog(t, logger)(mwtracing.Middleware(tracer)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})))

	req := httptest.NewRequest(http.MethodGet, "/span-log", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if len(*logger.entries) != 1 {
		t.Fatalf("expected one log entry, got %d", len(*logger.entries))
	}
	if (*logger.entries)[0].fields["span_id"] != "1234567890abcdef" {
		t.Fatalf("expected span id in access log, got %v", (*logger.entries)[0].fields["span_id"])
	}
}

func TestMiddlewareLogsOnPanic(t *testing.T) {
	logger := newStubLogger()
	handler := mustAccesslog(t, logger)(mwtracing.Middleware(&spanContextTracer{})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusAccepted)
		panic("boom")
	})))

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
	if len(*logger.entries) != 1 {
		t.Fatalf("expected one log entry, got %d", len(*logger.entries))
	}
	if got := (*logger.entries)[0].fields["status"]; got != http.StatusAccepted {
		t.Fatalf("logged status = %v, want %d", got, http.StatusAccepted)
	}
}

func TestMiddlewarePreservesDownstreamPanicWhenLoggerPanics(t *testing.T) {
	logger := newStubLogger()
	logger.panicOnRecord = true
	handler := mustAccesslog(t, logger)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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

func TestMiddlewareRejectsNilLogger(t *testing.T) {
	if _, err := Middleware(Config{}); !errors.Is(err, ErrNilLogger) {
		t.Fatalf("Middleware error = %v, want %v", err, ErrNilLogger)
	}
}

type hijackWriter struct {
	header   http.Header
	hijacked bool
}

func (w *hijackWriter) Header() http.Header {
	if w.header == nil {
		w.header = make(http.Header)
	}
	return w.header
}
func (w *hijackWriter) WriteHeader(_ int)           {}
func (w *hijackWriter) Write(p []byte) (int, error) { return len(p), nil }
func (w *hijackWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	w.hijacked = true
	return nil, nil, errors.New("hijack")
}

func TestMiddlewarePreservesHijacker(t *testing.T) {
	logger := newStubLogger()
	mw := mustAccesslog(t, logger)
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hj, ok := w.(http.Hijacker)
		if !ok {
			t.Fatalf("expected Hijacker support")
		}
		_, _, _ = hj.Hijack()
		w.WriteHeader(http.StatusSwitchingProtocols)
	})

	req := httptest.NewRequest(http.MethodGet, "/ws", nil)
	rec := &hijackWriter{}
	mw(handler).ServeHTTP(rec, req)

	if !rec.hijacked {
		t.Fatalf("expected hijack to be forwarded to underlying writer")
	}
}
