package conformance_test

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/birdordev/plumego/middleware"
	"github.com/birdordev/plumego/middleware/bodylimit"
	"github.com/birdordev/plumego/middleware/compression"
	"github.com/birdordev/plumego/middleware/debug"
	"github.com/birdordev/plumego/middleware/httpmetrics"
	"github.com/birdordev/plumego/middleware/singleflight"
	"github.com/birdordev/plumego/middleware/timeout"
	"github.com/birdordev/plumego/middleware/tracing"
)

func TestResponseWriterConformancePanicPropagation(t *testing.T) {
	tests := []struct {
		name string
		mw   middleware.Middleware
	}{
		{name: "bodylimit", mw: bodylimit.Middleware(bodylimit.Config{MaxBytes: 1024})},
		{name: "coalesce", mw: singleflight.New(singleflight.Config{Timeout: time.Second}).Middleware()},
		{name: "compression", mw: compression.Middleware(compression.Config{})},
		{name: "timeout", mw: timeout.Middleware(timeout.Config{Timeout: time.Second})},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			handler := tc.mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				panic("conformance panic")
			}))
			req := httptest.NewRequest(http.MethodGet, "/panic", nil)
			req.Header.Set("Accept-Encoding", "gzip")

			defer func() {
				if rec := recover(); rec != "conformance panic" {
					t.Fatalf("panic = %v, want conformance panic", rec)
				}
			}()
			handler.ServeHTTP(httptest.NewRecorder(), req)
		})
	}
}

func TestResponseWriterConformanceFlushForwarding(t *testing.T) {
	tests := []struct {
		name string
		mw   middleware.Middleware
	}{
		{name: "bodylimit", mw: bodylimit.Middleware(bodylimit.Config{MaxBytes: 1024})},
		{name: "coalesce", mw: singleflight.New(singleflight.Config{Timeout: time.Second}).Middleware()},
		{name: "compression", mw: compression.Middleware(compression.Config{})},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			writer := &conformanceFlushWriter{ResponseRecorder: httptest.NewRecorder()}
			handler := tc.mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				flusher, ok := w.(http.Flusher)
				if !ok {
					t.Fatalf("%s wrapper does not expose http.Flusher", tc.name)
				}
				flusher.Flush()
			}))
			req := httptest.NewRequest(http.MethodGet, "/flush", nil)
			req.Header.Set("Accept-Encoding", "gzip")

			handler.ServeHTTP(writer, req)

			if writer.flushed == 0 {
				t.Fatalf("%s wrapper did not forward Flush", tc.name)
			}
		})
	}
}

func TestResponseWriterConformanceGzipHijackBeforeCompression(t *testing.T) {
	writer := &conformanceHijackWriter{}
	handler := compression.Middleware(compression.Config{})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hijacker, ok := w.(http.Hijacker)
		if !ok {
			t.Fatal("gzip wrapper does not expose http.Hijacker before compression")
		}
		if _, _, err := hijacker.Hijack(); err != nil {
			t.Fatalf("Hijack returned error: %v", err)
		}
	}))
	req := httptest.NewRequest(http.MethodGet, "/hijack", nil)
	req.Header.Set("Accept-Encoding", "gzip")

	handler.ServeHTTP(writer, req)

	if !writer.hijacked {
		t.Fatal("underlying writer was not hijacked")
	}
}

func TestResponseWriterConformancePostTimeoutWriteReturnsDeadline(t *testing.T) {
	type writeResult struct {
		n   int
		err error
	}
	result := make(chan writeResult, 1)

	handler := timeout.Middleware(timeout.Config{Timeout: 10 * time.Millisecond})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(40 * time.Millisecond)
		n, err := w.Write([]byte("late"))
		result <- writeResult{n: n, err: err}
	}))

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/timeout", nil))

	if rec.Code != http.StatusGatewayTimeout {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusGatewayTimeout)
	}
	select {
	case got := <-result:
		if got.n != 0 {
			t.Fatalf("post-timeout write bytes = %d, want 0", got.n)
		}
		if !errors.Is(got.err, context.DeadlineExceeded) {
			t.Fatalf("post-timeout write error = %v, want context deadline exceeded", got.err)
		}
	case <-time.After(time.Second):
		t.Fatal("handler did not attempt post-timeout write")
	}
}

func TestResponseWriterConformanceGzipPartialPanicFinalizesStream(t *testing.T) {
	handler := compression.Middleware(compression.Config{})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte("partial"))
		panic("boom")
	}))
	req := httptest.NewRequest(http.MethodGet, "/partial", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	rec := httptest.NewRecorder()

	func() {
		defer func() {
			if recover() == nil {
				t.Fatal("expected panic")
			}
		}()
		handler.ServeHTTP(rec, req)
	}()

	if rec.Header().Get("Content-Encoding") != "gzip" {
		t.Fatalf("Content-Encoding = %q, want gzip", rec.Header().Get("Content-Encoding"))
	}
	if got := conformanceGunzip(t, rec.Body.Bytes()); got != "partial" {
		t.Fatalf("decompressed body = %q, want partial", got)
	}
}

func TestResponseWriterConformanceGzipFlushBeforeWritePassesThrough(t *testing.T) {
	handler := compression.Middleware(compression.Config{})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		flusher, ok := w.(http.Flusher)
		if !ok {
			t.Fatal("gzip wrapper does not expose http.Flusher")
		}
		flusher.Flush()
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte("after flush"))
	}))
	req := httptest.NewRequest(http.MethodGet, "/flush-write", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if !rec.Flushed {
		t.Fatal("underlying recorder was not flushed")
	}
	if rec.Header().Get("Content-Encoding") != "" {
		t.Fatalf("Content-Encoding = %q, want empty", rec.Header().Get("Content-Encoding"))
	}
	if rec.Body.String() != "after flush" {
		t.Fatalf("body = %q, want after flush", rec.Body.String())
	}
}

func TestResponseWriterConformanceOptionalInterfaceMatrix(t *testing.T) {
	tests := []struct {
		name      string
		mw        middleware.Middleware
		hasUnwrap bool
		hasFlush  bool
		hasHijack bool
		needsGzip bool
	}{
		{
			name:      "accesslog",
			mw:        newConformanceAccessLog(t),
			hasUnwrap: true,
			hasFlush:  true,
			hasHijack: true,
		},
		{name: "bodylimit", mw: bodylimit.Middleware(bodylimit.Config{MaxBytes: 1024}), hasUnwrap: true, hasFlush: true, hasHijack: true},
		{name: "coalesce", mw: singleflight.New(singleflight.Config{Timeout: time.Second}).Middleware(), hasUnwrap: true, hasFlush: true, hasHijack: true},
		{name: "compression", mw: compression.Middleware(compression.Config{}), hasUnwrap: true, hasFlush: true, hasHijack: true, needsGzip: true},
		{name: "debug", mw: debug.Middleware(debug.DefaultConfig()), hasUnwrap: true, hasFlush: true, hasHijack: true},
		{name: "httpmetrics", mw: httpmetrics.Middleware(conformanceObserver{}), hasUnwrap: true, hasFlush: true, hasHijack: true},
		{name: "recovery", mw: newConformanceRecovery(t), hasUnwrap: true, hasFlush: true, hasHijack: true},
		{name: "timeout", mw: timeout.Middleware(timeout.Config{Timeout: time.Second})},
		{name: "tracing", mw: tracing.Middleware(&conformanceTracer{}), hasUnwrap: true, hasFlush: true, hasHijack: true},
	}

	for _, tc := range tests {
		t.Run(tc.name+"/unwrap", func(t *testing.T) {
			result := make(chan bool, 1)
			handler := tc.mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, ok := w.(interface{ Unwrap() http.ResponseWriter })
				result <- ok
			}))
			handler.ServeHTTP(httptest.NewRecorder(), conformanceRequest(tc.needsGzip))
			if got := <-result; got != tc.hasUnwrap {
				t.Fatalf("Unwrap exposure = %v, want %v", got, tc.hasUnwrap)
			}
		})

		t.Run(tc.name+"/flush", func(t *testing.T) {
			result := make(chan bool, 1)
			writer := &conformanceFlushWriter{ResponseRecorder: httptest.NewRecorder()}
			handler := tc.mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				flusher, ok := w.(http.Flusher)
				if ok {
					flusher.Flush()
				}
				result <- ok
			}))
			handler.ServeHTTP(writer, conformanceRequest(tc.needsGzip))
			if got := <-result; got != tc.hasFlush {
				t.Fatalf("Flush exposure = %v, want %v", got, tc.hasFlush)
			}
			if tc.hasFlush && writer.flushed == 0 {
				t.Fatal("Flush was exposed but did not reach the underlying writer")
			}
		})

		t.Run(tc.name+"/hijack", func(t *testing.T) {
			result := make(chan bool, 1)
			writer := &conformanceHijackWriter{}
			handler := tc.mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				hijacker, ok := w.(http.Hijacker)
				if ok {
					_, _, _ = hijacker.Hijack()
				}
				result <- ok
			}))
			handler.ServeHTTP(writer, conformanceRequest(tc.needsGzip))
			if got := <-result; got != tc.hasHijack {
				t.Fatalf("Hijack exposure = %v, want %v", got, tc.hasHijack)
			}
			if tc.hasHijack && !writer.hijacked {
				t.Fatal("Hijack was exposed but did not reach the underlying writer")
			}
		})
	}
}

type conformanceFlushWriter struct {
	*httptest.ResponseRecorder
	flushed int
}

func (w *conformanceFlushWriter) Flush() {
	w.flushed++
	w.ResponseRecorder.Flush()
}

type conformanceHijackWriter struct {
	header   http.Header
	hijacked bool
}

func (w *conformanceHijackWriter) Header() http.Header {
	if w.header == nil {
		w.header = make(http.Header)
	}
	return w.header
}

func (w *conformanceHijackWriter) WriteHeader(int) {}

func (w *conformanceHijackWriter) Write(p []byte) (int, error) {
	return len(p), nil
}

func (w *conformanceHijackWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	w.hijacked = true
	var rw bytes.Buffer
	return nil, bufio.NewReadWriter(bufio.NewReader(&rw), bufio.NewWriter(&rw)), nil
}

func conformanceGunzip(t *testing.T, body []byte) string {
	t.Helper()

	reader, err := gzip.NewReader(bytes.NewReader(body))
	if err != nil {
		t.Fatalf("open gzip reader: %v", err)
	}
	defer reader.Close()

	decoded, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("read gzip body: %v", err)
	}
	return string(decoded)
}

func conformanceRequest(needsGzip bool) *http.Request {
	req := httptest.NewRequest(http.MethodGet, "/matrix", nil)
	if needsGzip {
		req.Header.Set("Accept-Encoding", "gzip")
	}
	return req
}

type conformanceObserver struct{}

func (conformanceObserver) ObserveHTTP(context.Context, string, string, int, int, time.Duration) {}

type conformanceTracer struct{}

func (*conformanceTracer) Start(ctx context.Context, r *http.Request) (context.Context, tracing.TraceSpan) {
	return ctx, conformanceSpan{}
}

type conformanceSpan struct{}

func (conformanceSpan) End(int, int, string) {}
func (conformanceSpan) TraceID() string      { return "" }
func (conformanceSpan) SpanID() string       { return "" }
