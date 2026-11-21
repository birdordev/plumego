package tracing

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/birdordev/plumego/contract"
)

type failingMetadataSpan struct {
	field string
	ends  int
}

func (s *failingMetadataSpan) TraceID() string {
	if s.field == "trace" {
		panic("trace identity failed")
	}
	return ""
}
func (s *failingMetadataSpan) SpanID() string {
	if s.field == "span" {
		panic("span identity failed")
	}
	return ""
}
func (s *failingMetadataSpan) End(int, int, string) { s.ends++ }

type metadataTracer struct {
	span       *failingMetadataSpan
	nilContext bool
}

func (t metadataTracer) Start(ctx context.Context, r *http.Request) (context.Context, TraceSpan) {
	if t.nilContext {
		return nil, t.span
	}
	return ctx, t.span
}

func TestTracingFailuresPreserveRequestAndCallNextOnce(t *testing.T) {
	for _, field := range []string{"trace", "span", "nil_context"} {
		t.Run(field, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			ctx = contract.WithRequestID(ctx, "trace-request")
			span := &failingMetadataSpan{field: field}
			calls := 0
			handler := Middleware(metadataTracer{span: span, nilContext: field == "nil_context"})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if r.Context().Done() != ctx.Done() || contract.RequestIDFromContext(r.Context()) != "trace-request" {
					t.Error("tracing failure replaced the request context")
				}
				w.WriteHeader(http.StatusAccepted)
			}))
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil).WithContext(ctx))
			if calls != 1 || span.ends != 1 || rec.Code != http.StatusAccepted {
				t.Fatalf("calls=%d ends=%d status=%d", calls, span.ends, rec.Code)
			}
		})
	}
}

type measuredSpan struct {
	failingMetadataSpan
	status, bytes int
}

func (s *measuredSpan) End(status, bytes int, _ string) {
	s.ends++
	s.status, s.bytes = status, bytes
}

type measuredTracer struct{ span *measuredSpan }

func (t measuredTracer) Start(ctx context.Context, _ *http.Request) (context.Context, TraceSpan) {
	return ctx, t.span
}

type partialTraceWriter struct{ *httptest.ResponseRecorder }

func (w partialTraceWriter) Write(p []byte) (int, error) {
	n, _ := w.ResponseRecorder.Write(p[:2])
	return n, io.ErrClosedPipe
}

func TestTracingRecordsPartialWriteOnErrorExactlyOnce(t *testing.T) {
	span := new(measuredSpan)
	handler := Middleware(measuredTracer{span})(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusAccepted)
		if n, err := w.Write([]byte("fixture")); n != 2 || !errors.Is(err, io.ErrClosedPipe) {
			t.Errorf("write=%d err=%v", n, err)
		}
	}))
	w := partialTraceWriter{httptest.NewRecorder()}
	handler.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/", nil))
	if span.ends != 1 || span.status != http.StatusAccepted || span.bytes != 2 || w.Body.String() != "fi" {
		t.Fatalf("span=%+v body=%q", span, w.Body)
	}
}
