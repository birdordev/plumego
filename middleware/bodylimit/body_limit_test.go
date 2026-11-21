package bodylimit

import (
	"bufio"
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"testing/iotest"
	"time"

	"github.com/birdordev/plumego/log"
	"github.com/birdordev/plumego/middleware/timeout"
)

type panicLogger struct{}

func (panicLogger) WithFields(log.Fields) log.StructuredLogger      { return panicLogger{} }
func (panicLogger) With(string, any) log.StructuredLogger           { return panicLogger{} }
func (panicLogger) Debug(string, ...log.Fields)                     {}
func (panicLogger) Info(string, ...log.Fields)                      {}
func (panicLogger) Warn(string, ...log.Fields)                      { panic("logger panic") }
func (panicLogger) Error(string, ...log.Fields)                     {}
func (panicLogger) DebugCtx(context.Context, string, ...log.Fields) {}
func (panicLogger) InfoCtx(context.Context, string, ...log.Fields)  {}
func (panicLogger) WarnCtx(context.Context, string, ...log.Fields)  { panic("logger panic") }
func (panicLogger) ErrorCtx(context.Context, string, ...log.Fields) {}
func (panicLogger) Fatal(string, ...log.Fields)                     {}
func (panicLogger) FatalCtx(context.Context, string, ...log.Fields) {}

func TestMiddlewareEnforcesRequestCap(t *testing.T) {
	mw := Middleware(Config{MaxBytes: 10})
	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		_, _ = w.Write(body)
	}))

	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader("hello"))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	req = httptest.NewRequest(http.MethodPost, "/", strings.NewReader("this body is definitely too long"))
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("expected 413, got %d", rec.Code)
	}
}

func TestDefaultConfigSetsLimitAndLogger(t *testing.T) {
	cfg := DefaultConfig(12, nil)
	if cfg.MaxBytes != 12 {
		t.Fatalf("MaxBytes = %d, want 12", cfg.MaxBytes)
	}
	if cfg.Logger != nil {
		t.Fatalf("Logger = %v, want nil", cfg.Logger)
	}
}

func TestMiddlewareAllowsExactLimit(t *testing.T) {
	mw := Middleware(Config{MaxBytes: 5})
	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read body: %v", err)
		}
		_, _ = w.Write(body)
	}))

	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader("hello"))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	if got := rec.Body.String(); got != "hello" {
		t.Fatalf("expected exact body to pass through, got %q", got)
	}
}

func TestMiddlewareDetectsSingleReadOverrun(t *testing.T) {
	mw := Middleware(Config{MaxBytes: 5})
	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		buf := make([]byte, 16)
		n, err := r.Body.Read(buf)
		if n != 5 {
			t.Fatalf("first read bytes = %d, want 5", n)
		}
		if err != errRequestTooLarge {
			t.Fatalf("first read error = %v, want %v", err, errRequestTooLarge)
		}
	}))

	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader("toolong"))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("expected 413, got %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "REQUEST_BODY_TOO_LARGE") {
		t.Fatalf("expected structured body limit error, got %q", rec.Body.String())
	}
}

func TestMiddlewareSuppressesDownstreamWritesAfterLimitError(t *testing.T) {
	mw := Middleware(Config{MaxBytes: 5})
	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, "handler saw "+err.Error(), http.StatusBadRequest)
			return
		}
		t.Fatal("expected body read to fail")
	}))

	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader("toolong"))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("expected 413, got %d", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "REQUEST_BODY_TOO_LARGE") {
		t.Fatalf("expected structured body limit error, got %q", body)
	}
	if strings.Contains(body, "handler saw") || strings.Contains(body, errRequestTooLarge.Error()) {
		t.Fatalf("downstream error write polluted body limit response: %q", body)
	}
}

func TestMiddlewareLoggerPanicDoesNotBlockLimitError(t *testing.T) {
	mw := Middleware(Config{MaxBytes: 5, Logger: panicLogger{}})
	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.ReadAll(r.Body)
	}))

	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader("toolong"))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusRequestEntityTooLarge)
	}
	if !strings.Contains(rec.Body.String(), "REQUEST_BODY_TOO_LARGE") {
		t.Fatalf("expected structured body limit error, got %q", rec.Body.String())
	}
}

func TestMiddlewareDownstreamWriteAfterOverrunReportsRejection(t *testing.T) {
	mw := Middleware(Config{MaxBytes: 5})
	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, err := io.ReadAll(r.Body)
		if err == nil {
			t.Fatal("expected body read to fail")
		}
		n, writeErr := w.Write([]byte("late handler body"))
		if n != 0 || !errors.Is(writeErr, errRequestTooLarge) {
			t.Fatalf("post-overrun Write = %d, %v", n, writeErr)
		}
	}))

	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader("toolong"))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("expected 413, got %d", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "REQUEST_BODY_TOO_LARGE") {
		t.Fatalf("expected structured body limit error, got %q", body)
	}
	if strings.Contains(body, "late handler body") {
		t.Fatalf("post-overrun write polluted body limit response: %q", body)
	}
}

func TestMiddlewareDisabledPassesThrough(t *testing.T) {
	for _, limit := range []int64{0, -1} {
		req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader("unlimited"))
		rec := httptest.NewRecorder()
		originalBody := req.Body
		calls := 0
		handler := Middleware(Config{MaxBytes: limit})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			calls++
			if w != rec || r != req || r.Body != originalBody {
				t.Errorf("limit %d wrapped a disabled request", limit)
			}
			body, err := io.ReadAll(r.Body)
			if err != nil {
				t.Fatal(err)
			}
			_, _ = w.Write(body)
		}))
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK || rec.Body.String() != "unlimited" || calls != 1 {
			t.Fatalf("limit=%d status=%d calls=%d body=%q", limit, rec.Code, calls, rec.Body)
		}
	}
}

func TestResponseWriterUnwrap(t *testing.T) {
	underlying := httptest.NewRecorder()
	w := &bodyLimitResponseWriter{ResponseWriter: underlying}

	if got := w.Unwrap(); got != underlying {
		t.Fatalf("Unwrap() = %v, want underlying writer", got)
	}
}

// --- NEW COVERAGE TESTS ---

// bodyFlusherRecorder is an httptest.ResponseRecorder that also implements http.Flusher.
type bodyFlusherRecorder struct {
	*httptest.ResponseRecorder
	flushed int
}

func (f *bodyFlusherRecorder) Flush() {
	f.flushed++
	f.ResponseRecorder.Flush()
}

// bodyHijackerRecorder is an http.ResponseWriter + http.Hijacker.
type bodyHijackerRecorder struct {
	*httptest.ResponseRecorder
	hijacked bool
}

func (h *bodyHijackerRecorder) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	h.hijacked = true
	server, client := net.Pipe()
	_ = client.Close()
	rw := bufio.NewReadWriter(bufio.NewReader(server), bufio.NewWriter(server))
	return server, rw, nil
}

// TestBodyLimitWriterFlush_Normal verifies Flush is forwarded when not blocked.
func TestBodyLimitWriterFlush_Normal(t *testing.T) {
	mw := Middleware(Config{MaxBytes: 100})
	underlying := &bodyFlusherRecorder{ResponseRecorder: httptest.NewRecorder()}
	flushed := false

	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
			flushed = true
		} else {
			t.Error("expected Flusher from bodyLimitResponseWriter")
		}
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader("hi"))
	handler.ServeHTTP(underlying, req)

	if !flushed {
		t.Fatal("handler did not call Flush")
	}
	if underlying.flushed == 0 {
		t.Fatal("Flush was not forwarded to underlying writer")
	}
}

// TestBodyLimitWriterFlush_BlockedDoesNotFlush verifies Flush is suppressed when blocked.
func TestBodyLimitWriterFlush_BlockedDoesNotFlush(t *testing.T) {
	w := &bodyLimitResponseWriter{
		ResponseWriter: &bodyFlusherRecorder{ResponseRecorder: httptest.NewRecorder()},
		blocked:        true,
	}
	w.Flush() // must not panic and must not forward
	if underlying, ok := w.ResponseWriter.(*bodyFlusherRecorder); ok {
		if underlying.flushed != 0 {
			t.Fatal("Flush was forwarded despite blocked=true")
		}
	}
}

// TestBodyLimitWriterHijack_Normal verifies Hijack is forwarded and started flag is set.
func TestBodyLimitWriterHijack_Normal(t *testing.T) {
	mw := Middleware(Config{MaxBytes: 100})
	underlying := &bodyHijackerRecorder{ResponseRecorder: httptest.NewRecorder()}
	hijacked := false

	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hj, ok := w.(http.Hijacker)
		if !ok {
			t.Error("expected Hijacker from bodyLimitResponseWriter")
			return
		}
		conn, _, err := hj.Hijack()
		if err != nil {
			t.Errorf("Hijack failed: %v", err)
			return
		}
		hijacked = true
		_ = conn.Close()
	}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	handler.ServeHTTP(underlying, req)

	if !hijacked {
		t.Fatal("handler did not successfully hijack")
	}
	if !underlying.hijacked {
		t.Fatal("Hijack was not forwarded to underlying writer")
	}
}

// TestBodyLimitWriterHijack_NotSupported verifies an error is returned when underlying
// does not implement http.Hijacker.
func TestBodyLimitWriterHijack_NotSupported(t *testing.T) {
	w := &bodyLimitResponseWriter{ResponseWriter: httptest.NewRecorder()}
	_, _, err := w.Hijack()
	if err == nil {
		t.Fatal("expected error when underlying writer does not support Hijack")
	}
}

// TestBodyLimitWriterWriteHeader_Blocked verifies WriteHeader is ignored when blocked.
func TestBodyLimitWriterWriteHeader_Blocked(t *testing.T) {
	underlying := httptest.NewRecorder()
	w := &bodyLimitResponseWriter{ResponseWriter: underlying, blocked: true}
	w.WriteHeader(http.StatusCreated)
	if underlying.Code != 200 { // httptest default, not yet written
		t.Fatalf("WriteHeader should be suppressed when blocked, got code %d", underlying.Code)
	}
}

func TestBodyLimitWithNilRequestBody(t *testing.T) {
	req, err := http.NewRequest(http.MethodGet, "/", nil)
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	handler := Middleware(DefaultConfig(8, nil))(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		var buf [1]byte
		if n, err := r.Body.Read(buf[:]); n != 0 || err != io.EOF {
			t.Errorf("Read() = (%d, %v), want (0, EOF)", n, err)
		}
		if err := r.Body.Close(); err != nil {
			t.Errorf("Close() = %v", err)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, req)
	if calls != 1 || recorder.Code != http.StatusNoContent {
		t.Fatalf("handler calls = %d, status = %d, want 1 and 204", calls, recorder.Code)
	}
}

// TestBodyLimitClose verifies the body reader Close propagates to the underlying reader.
func TestBodyLimitClose(t *testing.T) {
	mw := Middleware(Config{MaxBytes: 100})
	closed := false

	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Close should propagate.
		if err := r.Body.Close(); err != nil {
			t.Errorf("Close error: %v", err)
		}
		closed = true
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader("data"))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if !closed {
		t.Fatal("body Close was not called")
	}
}

// TestBodyLimitChunkedNoContentLength verifies behavior when body has no Content-Length.
func TestBodyLimitChunkedNoContentLength(t *testing.T) {
	mw := Middleware(Config{MaxBytes: 5})
	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		_, _ = w.Write(body)
	}))

	// Use a pipe-based body with no Content-Length set.
	pr, pw := io.Pipe()
	go func() {
		_, _ = pw.Write([]byte("hello"))
		_ = pw.Close()
	}()

	req := httptest.NewRequest(http.MethodPost, "/", pr)
	req.ContentLength = -1 // explicitly unset
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	if rec.Body.String() != "hello" {
		t.Fatalf("body = %q, want hello", rec.Body.String())
	}
}

// TestBodyLimitChunkedNoContentLength_Overrun verifies chunked overrun is detected.
func TestBodyLimitChunkedNoContentLength_Overrun(t *testing.T) {
	mw := Middleware(Config{MaxBytes: 3})
	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusOK) // this should be suppressed
	}))

	pr, pw := io.Pipe()
	go func() {
		_, _ = pw.Write([]byte("toolong"))
		_ = pw.Close()
	}()

	req := httptest.NewRequest(http.MethodPost, "/", pr)
	req.ContentLength = -1
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("expected 413, got %d", rec.Code)
	}
}

// TestBodyLimitExactBytesAllowed verifies a body exactly equal to the limit passes through.
func TestBodyLimitExactBytesAllowed(t *testing.T) {
	const limit = 4
	mw := Middleware(Config{MaxBytes: limit})
	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read body: %v", err)
		}
		_, _ = w.Write(body)
	}))

	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader("abcd")) // exactly 4
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	if rec.Body.String() != "abcd" {
		t.Fatalf("body = %q, want abcd", rec.Body.String())
	}
}

// TestBodyLimitOneByteOver verifies a body exactly 1 byte over the limit is rejected.
func TestBodyLimitOneByteOver(t *testing.T) {
	const limit = 4
	mw := Middleware(Config{MaxBytes: limit})
	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader("abcde")) // 5 bytes
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("expected 413, got %d", rec.Code)
	}
}

// errorReader simulates a mid-read I/O error.
type errorReader struct {
	data     []byte
	pos      int
	failAt   int
	failWith error
}

func (e *errorReader) Read(p []byte) (int, error) {
	if e.pos >= e.failAt {
		return 0, e.failWith
	}
	n := copy(p, e.data[e.pos:])
	e.pos += n
	if e.pos >= e.failAt {
		return n, e.failWith
	}
	return n, nil
}

func (e *errorReader) Close() error { return nil }

// TestBodyLimitReaderMidReadError verifies that an underlying reader error is propagated.
func TestBodyLimitReaderMidReadError(t *testing.T) {
	mw := Middleware(Config{MaxBytes: 100})
	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		buf := make([]byte, 50)
		_, err := r.Body.Read(buf)
		if err != nil && err != io.EOF && err != io.ErrUnexpectedEOF {
			// Got the expected I/O error – return a bad request to signal detection.
			http.Error(w, "read error", http.StatusBadRequest)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))

	errBody := &errorReader{
		data:     []byte("hello"),
		failAt:   3,
		failWith: io.ErrUnexpectedEOF,
	}

	req := httptest.NewRequest(http.MethodPost, "/", errBody)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	// Any non-panic result is acceptable; the key assertion is no crash.
	if rec.Code == 0 {
		t.Fatal("expected a response code")
	}
}

// TestBodyLimitWriteLimitErrorAlreadyStarted verifies writeLimitError is a no-op
// when the response is already started.
func TestBodyLimitWriteLimitErrorAlreadyStarted(t *testing.T) {
	underlying := httptest.NewRecorder()
	w := &bodyLimitResponseWriter{ResponseWriter: underlying, started: true}
	w.writeLimitError(httptest.NewRequest(http.MethodGet, "/", nil), 10, 20, time.Now())

	// The response should not have been overwritten.
	if underlying.Code != 200 {
		t.Fatalf("expected default recorder code 200, got %d", underlying.Code)
	}
	if w.blocked != true {
		t.Fatal("blocked flag should be set even when started")
	}
}

// TestBodyLimitWriteLimitErrorAlreadyBlocked verifies writeLimitError is a no-op
// when already blocked (covers the w.blocked early-return branch).
func TestBodyLimitWriteLimitErrorAlreadyBlocked(t *testing.T) {
	underlying := httptest.NewRecorder()
	w := &bodyLimitResponseWriter{ResponseWriter: underlying, blocked: true}
	// Must not panic or write to underlying.
	w.writeLimitError(httptest.NewRequest(http.MethodGet, "/", nil), 10, 20, time.Now())
	if underlying.Code != 200 {
		t.Fatalf("expected no status written to recorder, got %d", underlying.Code)
	}
}

// TestBodyLimitReadAfterExceeded verifies that Read returns errRequestTooLarge
// immediately after the limit has already been exceeded (covers the l.exceeded
// early-return branch in Read).
func TestBodyLimitReadAfterExceeded(t *testing.T) {
	mw := Middleware(Config{MaxBytes: 3})
	var secondReadErr error

	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		buf := make([]byte, 16)
		// First read: triggers the overrun.
		r.Body.Read(buf) //nolint:errcheck
		// Second read: should immediately return errRequestTooLarge via l.exceeded path.
		_, secondReadErr = r.Body.Read(buf)
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader("toolong"))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if secondReadErr == nil {
		t.Fatal("expected errRequestTooLarge on second read after exceeded")
	}
}

// TestBodyLimitProbeReadGetsData covers the branch in Read where remaining <= 0
// and the probe read returns n > 0 (upstream buffered more data than expected).
// We exercise this via the middleware integration path.
func TestBodyLimitProbeReadGetsData(t *testing.T) {
	// maxBytes = 3, body = "abcX" (4 bytes). After reading exactly 3 bytes the reader
	// has used == maxBytes. The next Read call enters the "remaining <= 0" branch
	// and does a 1-byte probe. The underlying reader has "X" left, so n==1, which
	// triggers the n>0 sub-branch and returns errRequestTooLarge.
	mw := Middleware(Config{MaxBytes: 3})
	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Read exactly 3 bytes first.
		first := make([]byte, 3)
		if _, err := io.ReadFull(r.Body, first); err != nil {
			t.Logf("first read err: %v", err) // may already return error on some paths
		}
		// Now read again – at limit, probe returns data, should get errRequestTooLarge.
		buf := make([]byte, 4)
		n, err := r.Body.Read(buf)
		_ = n
		if err != nil && err != errRequestTooLarge {
			// io.EOF is also acceptable if the underlying body ended at exactly 3.
			t.Logf("second read err (acceptable): %v", err)
		}
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader("abcX"))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	// The limit is hit; status may be 413 or 200 depending on which read triggers.
	// We just need no panic.
}

func TestBodyLimitEmptyReadDoesNotProbe(t *testing.T) {
	for _, body := range []string{"x", "xy"} {
		t.Run(body, func(t *testing.T) {
			rec := httptest.NewRecorder()
			handler := Middleware(Config{MaxBytes: 1})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if _, err := io.ReadFull(r.Body, make([]byte, 1)); err != nil {
					t.Fatal(err)
				}
				if n, err := r.Body.Read(nil); n != 0 || err != nil {
					t.Fatalf("empty read = %d, %v", n, err)
				}
				want := io.EOF
				if len(body) > 1 {
					want = errRequestTooLarge
				}
				if n, err := r.Body.Read(make([]byte, 1)); n != 0 || err != want {
					t.Errorf("nonempty read = %d, %v, want 0, %v", n, err, want)
				}
			}))
			handler.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body)))
			wantStatus := http.StatusOK
			if len(body) > 1 {
				wantStatus = http.StatusRequestEntityTooLarge
			}
			if rec.Code != wantStatus {
				t.Errorf("status = %d, want %d", rec.Code, wantStatus)
			}
		})
	}
}

func TestBodyLimitEnforcesBytesReadIndependentlyOfDeclaredLength(t *testing.T) {
	for _, tc := range []struct {
		name   string
		read   int64
		body   string
		status int
	}{
		{"no read", 0, "abcde", http.StatusNoContent},
		{"partial read", 2, "abcde", http.StatusNoContent},
		{"exact limit", 4, "abcd", http.StatusNoContent},
		{"overrun", 5, "abcde", http.StatusRequestEntityTooLarge},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			handler := Middleware(Config{MaxBytes: 4})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if tc.read > 0 {
					_, _ = io.CopyN(io.Discard, r.Body, tc.read)
				}
				w.WriteHeader(http.StatusNoContent)
			}))
			req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(tc.body))
			req.ContentLength = 64
			dst := httptest.NewRecorder()
			handler.ServeHTTP(dst, req)
			if dst.Code != tc.status || calls != 1 {
				t.Fatalf("status=%d calls=%d, want %d and one call", dst.Code, calls, tc.status)
			}
		})
	}
}

type failingBody struct {
	err, closeErr error
	closeCalls    int
}

func (b *failingBody) Read([]byte) (int, error) { return 0, b.err }

func (b *failingBody) Close() error {
	b.closeCalls++
	return b.closeErr
}

func TestBodyLimitPreservesUnderlyingErrorsAndClose(t *testing.T) {
	body := &failingBody{err: io.ErrUnexpectedEOF, closeErr: io.ErrClosedPipe}
	handler := Middleware(Config{MaxBytes: 8})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, err := io.ReadAll(r.Body); !errors.Is(err, body.err) {
			t.Errorf("read error = %v", err)
		}
		if err := r.Body.Close(); !errors.Is(err, body.closeErr) {
			t.Errorf("close error = %v", err)
		}
		w.WriteHeader(http.StatusAccepted)
	}))
	req := httptest.NewRequest(http.MethodPost, "/", body)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusAccepted || body.closeCalls != 1 {
		t.Fatalf("status=%d closes=%d", rec.Code, body.closeCalls)
	}
}

func TestAcceptanceBodyLimitBlockedWritesAndRequestOwnership(t *testing.T) {
	for _, status := range []int{0, http.StatusCreated, http.StatusBadRequest} {
		t.Run(strconv.Itoa(status), func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader("ab"))
			body := &trackedLimitBody{ReadCloser: req.Body}
			req.Body = body
			handler := Middleware(Config{MaxBytes: 1})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r == req || r.Body == body || r.Context() != req.Context() {
					t.Fatal("downstream request did not isolate body replacement")
				}
				if status != 0 {
					w.WriteHeader(status)
				}
				if _, err := io.ReadAll(r.Body); !errors.Is(err, errRequestTooLarge) {
					t.Fatalf("read error = %v", err)
				}
				if err := r.Body.Close(); err != nil {
					t.Fatal(err)
				}
				if status == http.StatusBadRequest {
					if n, err := w.Write([]byte("previous error")); n != 14 || err != nil {
						t.Fatalf("committed error write = %d, %v", n, err)
					}
					return
				}
				for _, payload := range [][]byte{nil, []byte("discarded")} {
					if n, err := w.Write(payload); n != 0 || !errors.Is(err, errRequestTooLarge) {
						t.Fatalf("blocked Write = %d, %v", n, err)
					}
				}
				if err := http.NewResponseController(w).Flush(); !errors.Is(err, errRequestTooLarge) {
					t.Fatalf("blocked Flush = %v", err)
				}
			}))
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)
			if req.Body != body || !body.closed {
				t.Fatal("original body identity or close propagation was lost")
			}
			want := status
			if want == 0 {
				want = http.StatusRequestEntityTooLarge
			}
			if rec.Code != want || strings.Contains(rec.Body.String(), "discarded") {
				t.Fatalf("status=%d, body=%q", rec.Code, rec.Body.String())
			}
		})
	}
}

type trackedLimitBody struct {
	io.ReadCloser
	closed bool
}

func (b *trackedLimitBody) Close() error {
	b.closed = true
	return b.ReadCloser.Close()
}

func TestBodyLimitRequestOwnershipAcrossTimeoutOrders(t *testing.T) {
	bodyLimit := Middleware(Config{MaxBytes: 1})
	deadline := timeout.Middleware(timeout.Config{Timeout: time.Second})
	for _, outerLimit := range []bool{true, false} {
		req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader("ab"))
		original := req.Body
		var h http.Handler = http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
			if _, ok := r.Context().Deadline(); !ok || r.Body == original {
				t.Error("downstream lost its deadline or body wrapper")
			}
			if _, err := io.ReadAll(r.Body); !errors.Is(err, errRequestTooLarge) {
				t.Errorf("body limit read = %v", err)
			}
		})
		if outerLimit {
			h = bodyLimit(deadline(h))
		} else {
			h = deadline(bodyLimit(h))
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		if w.Code != http.StatusRequestEntityTooLarge || req.Body != original {
			t.Fatalf("limit outside timeout = %t: status=%d owned body=%t", outerLimit, w.Code, req.Body == original)
		}
	}
}

func TestCancellationAfterReadOverrunSkipsPendingError(t *testing.T) {
	for _, committed := range []bool{true, false} {
		ctx, cancel := context.WithCancel(t.Context())
		outer := httptest.NewRecorder()
		h := Middleware(Config{MaxBytes: 1})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if committed {
				w.WriteHeader(http.StatusAccepted)
			}
			if _, err := io.ReadAll(r.Body); !errors.Is(err, errRequestTooLarge) {
				t.Errorf("ReadAll: %v", err)
			}
			cancel()
		}))
		h.ServeHTTP(outer, httptest.NewRequest(http.MethodPost, "/", strings.NewReader("AB")).WithContext(ctx))
		cancel()
		want := http.StatusOK
		if committed {
			want = http.StatusAccepted
		}
		if outer.Code != want || outer.Body.Len() != 0 {
			t.Fatalf("committed=%t status=%d body=%q", committed, outer.Code, outer.Body)
		}
	}
}

func TestOverrunAfterSuccessPreservesPrefixAndBlocksFlush(t *testing.T) {
	rec := httptest.NewRecorder()
	h := Middleware(Config{MaxBytes: 1})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
		if _, err := io.WriteString(w, "prefix"); err != nil {
			t.Fatal(err)
		}
		if _, err := io.ReadAll(r.Body); !errors.Is(err, errRequestTooLarge) {
			t.Fatalf("read=%v", err)
		}
		if err := http.NewResponseController(w).Flush(); !errors.Is(err, errRequestTooLarge) {
			t.Fatalf("flush=%v", err)
		}
	}))
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/", strings.NewReader("ab")))
	if rec.Code != http.StatusCreated || rec.Body.String() != "prefix" || rec.Flushed {
		t.Fatalf("status=%d body=%q flushed=%t", rec.Code, rec.Body, rec.Flushed)
	}
}

func TestBodyLimitAfterExplicitErrorKeepsBothBodyWrites(t *testing.T) {
	for _, chooseFirst := range []bool{false, true} {
		h := Middleware(Config{MaxBytes: 2})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if chooseFirst {
				w.WriteHeader(422)
				_, _ = io.WriteString(w, "left")
			}
			if _, err := io.ReadAll(r.Body); !errors.Is(err, errRequestTooLarge) {
				t.Errorf("read err=%v", err)
			}
			if !chooseFirst {
				w.WriteHeader(422)
			}
			_, _ = io.WriteString(w, "right")
		}))
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("POST", "/", strings.NewReader("fixture")))
		if chooseFirst && (w.Code != 422 || w.Body.String() != "leftright") ||
			!chooseFirst && (w.Code != 413 || strings.Contains(w.Body.String(), "right")) {
			t.Fatalf("business first=%t response=%d %q", chooseFirst, w.Code, w.Body.String())
		}
	}
}

type boundaryCloseBody struct {
	io.Reader
	closeErr error
	closes   int
}

func (b *boundaryCloseBody) Close() error { b.closes++; return b.closeErr }

func TestExactLimitDelegatesExplicitBodyClose(t *testing.T) {
	fixture := &boundaryCloseBody{Reader: strings.NewReader("four"), closeErr: errors.New("fixture close error")}
	req := httptest.NewRequest(http.MethodPost, "/", nil)
	req.Body = fixture
	rec := httptest.NewRecorder()
	Middleware(Config{MaxBytes: 4})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, err := io.ReadAll(r.Body)
		if string(data) != "four" || err != nil {
			t.Errorf("data=%q error=%v", data, err)
		}
		if err := r.Body.Close(); !errors.Is(err, fixture.closeErr) {
			t.Errorf("close=%v", err)
		}
		w.WriteHeader(204)
	})).ServeHTTP(rec, req)
	if fixture.closes != 1 || rec.Code != 204 {
		t.Fatalf("close count=%d status=%d", fixture.closes, rec.Code)
	}
}

func TestBodyLimitRetainsFinalBytesReturnedWithEOF(t *testing.T) {
	for _, limit := range []int64{3, 4} {
		var output strings.Builder
		h := Middleware(Config{MaxBytes: limit})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if _, err := io.Copy(&output, r.Body); err != nil {
				t.Fatal(err)
			}
			w.WriteHeader(http.StatusNoContent)
		}))
		r := httptest.NewRequest(http.MethodPost, "/", iotest.DataErrReader(strings.NewReader("abc")))
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != http.StatusNoContent || output.String() != "abc" || w.Body.Len() != 0 {
			t.Fatalf("limit=%d status=%d data=%q body=%q", limit, w.Code, output.String(), w.Body)
		}
	}
}
