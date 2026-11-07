package recovery

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/spcent/plumego/contract"
	internaltransport "github.com/spcent/plumego/internal/httputil"
	"github.com/spcent/plumego/log"
	"github.com/spcent/plumego/middleware"
)

type recordingLogger struct {
	mu            sync.Mutex
	errCount      int
	lastMsg       string
	last          log.Fields
	panicOnRecord bool
}

func (l *recordingLogger) WithFields(fields log.Fields) log.StructuredLogger {
	l.mu.Lock()
	defer l.mu.Unlock()
	cp := make(log.Fields, len(fields))
	for k, v := range fields {
		cp[k] = v
	}
	l.last = cp
	return l
}

func (l *recordingLogger) With(key string, value any) log.StructuredLogger {
	return l.WithFields(log.Fields{key: value})
}

func (l *recordingLogger) Debug(msg string, fields ...log.Fields) {}
func (l *recordingLogger) Info(msg string, fields ...log.Fields)  {}
func (l *recordingLogger) Warn(msg string, fields ...log.Fields)  {}
func (l *recordingLogger) Error(msg string, fields ...log.Fields) {
	if l.panicOnRecord {
		panic("logger panic")
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.errCount++
	l.lastMsg = msg
}

func (l *recordingLogger) DebugCtx(ctx context.Context, msg string, fields ...log.Fields) {}
func (l *recordingLogger) InfoCtx(ctx context.Context, msg string, fields ...log.Fields)  {}
func (l *recordingLogger) WarnCtx(ctx context.Context, msg string, fields ...log.Fields)  {}
func (l *recordingLogger) ErrorCtx(ctx context.Context, msg string, fields ...log.Fields) {}
func (l *recordingLogger) Fatal(msg string, fields ...log.Fields)                         {}
func (l *recordingLogger) FatalCtx(ctx context.Context, msg string, fields ...log.Fields) {}

func mustRecovery(tb testing.TB, logger log.StructuredLogger) middleware.Middleware {
	tb.Helper()
	mw, err := Middleware(Config{Logger: logger})
	if err != nil {
		tb.Fatalf("Middleware returned error: %v", err)
	}
	return mw
}

func TestRecoveryMiddleware(t *testing.T) {
	logger := log.NewLogger(log.LoggerConfig{Format: log.LoggerFormatDiscard})
	tests := []struct {
		name           string
		handler        http.Handler
		shouldPanic    bool
		expectedStatus int
	}{
		{
			name: "Normal handler execution - no panic",
			handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte("success"))
			}),
			shouldPanic:    false,
			expectedStatus: http.StatusOK,
		},
		{
			name: "Handler panics with string",
			handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				panic("something went wrong")
			}),
			shouldPanic:    true,
			expectedStatus: http.StatusInternalServerError,
		},
		{
			name: "Handler panics with error",
			handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				panic(http.ErrMissingFile)
			}),
			shouldPanic:    true,
			expectedStatus: http.StatusInternalServerError,
		},
		{
			name: "Handler panics with integer",
			handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				panic(42)
			}),
			shouldPanic:    true,
			expectedStatus: http.StatusInternalServerError,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/test", nil)
			w := httptest.NewRecorder()

			recoveryHandler := mustRecovery(t, logger)(tt.handler)
			recoveryHandler.ServeHTTP(w, req)

			if w.Code != tt.expectedStatus {
				t.Errorf("expected status %d, got %d", tt.expectedStatus, w.Code)
			}

			if tt.shouldPanic {
				var response struct {
					Error struct {
						Code     string                 `json:"code"`
						Message  string                 `json:"message"`
						Category contract.ErrorCategory `json:"category"`
						Type     contract.ErrorType     `json:"type,omitempty"`
						Details  map[string]any         `json:"details,omitempty"`
					} `json:"error"`
					RequestID string `json:"request_id,omitempty"`
				}
				if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
					t.Fatalf("failed to unmarshal response: %v", err)
				}
				if response.Error.Code != contract.CodeInternalError {
					t.Errorf("expected error code %s, got %s", contract.CodeInternalError, response.Error.Code)
				}
				if response.Error.Category != contract.CategoryServer {
					t.Errorf("expected server error category, got %s", response.Error.Category)
				}
			} else if body := w.Body.String(); body != "success" {
				t.Errorf("expected success body, got %q", body)
			}
		})
	}
}

// Test that recovery middleware doesn't interfere with normal responses
func TestRecoveryMiddleware_NormalFlow(t *testing.T) {
	logger := log.NewLogger(log.LoggerConfig{Format: log.LoggerFormatDiscard})
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Custom-Header", "test-value")
		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte("custom response"))
	})

	req := httptest.NewRequest(http.MethodPost, "/test", nil)
	w := httptest.NewRecorder()

	recoveryHandler := mustRecovery(t, logger)(handler)
	recoveryHandler.ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("expected status 201, got %d", w.Code)
	}

	if w.Header().Get("Custom-Header") != "test-value" {
		t.Errorf("custom header not preserved")
	}

	if w.Header().Get("Content-Type") != "text/plain" {
		t.Errorf("content-type header not preserved")
	}

	if body := w.Body.String(); body != "custom response" {
		t.Errorf("response body not preserved, got %q", body)
	}
}

func TestRecoveryMiddleware_DoesNotAppendErrorAfterResponseStarted(t *testing.T) {
	logger := log.NewLogger(log.LoggerConfig{Format: log.LoggerFormatDiscard})
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte("partial"))
		panic("late panic")
	})

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	w := httptest.NewRecorder()

	recoveryHandler := mustRecovery(t, logger)(handler)
	recoveryHandler.ServeHTTP(w, req)

	if w.Code != http.StatusAccepted {
		t.Fatalf("expected original status 202, got %d", w.Code)
	}
	if body := w.Body.String(); body != "partial" {
		t.Fatalf("expected partial response without appended error, got %q", body)
	}
}

func TestRecoveryMiddleware_WritesErrorBeforeResponseStarted(t *testing.T) {
	logger := log.NewLogger(log.LoggerConfig{Format: log.LoggerFormatDiscard})
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		panic("early panic")
	})

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	w := httptest.NewRecorder()

	recoveryHandler := mustRecovery(t, logger)(handler)
	recoveryHandler.ServeHTTP(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("expected status 500, got %d", w.Code)
	}
	var response struct {
		Error struct {
			Code     string                 `json:"code"`
			Message  string                 `json:"message"`
			Category contract.ErrorCategory `json:"category"`
			Type     contract.ErrorType     `json:"type,omitempty"`
			Details  map[string]any         `json:"details,omitempty"`
		} `json:"error"`
		RequestID string `json:"request_id,omitempty"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}
	if response.Error.Code != contract.CodeInternalError {
		t.Fatalf("expected code %s, got %s", contract.CodeInternalError, response.Error.Code)
	}
}

func TestRecoveryMiddleware_Concurrent(t *testing.T) {
	logger := log.NewLogger(log.LoggerConfig{Format: log.LoggerFormatDiscard})
	panicHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		panic("concurrent panic")
	})
	recoveryHandler := mustRecovery(t, logger)(panicHandler)

	const numRequests = 10
	var wg sync.WaitGroup
	for i := 0; i < numRequests; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			req := httptest.NewRequest(http.MethodGet, "/panic", nil)
			w := httptest.NewRecorder()
			recoveryHandler.ServeHTTP(w, req)
			if w.Code != http.StatusInternalServerError {
				t.Errorf("expected status 500, got %d", w.Code)
			}
		}()
	}

	wg.Wait()
}

// Test that middleware properly handles different HTTP methods
func TestRecoveryMiddleware_DifferentMethods(t *testing.T) {
	logger := log.NewLogger(log.LoggerConfig{Format: log.LoggerFormatDiscard})
	methods := []string{http.MethodGet, http.MethodPost, http.MethodPut, http.MethodDelete, http.MethodPatch, http.MethodHead, http.MethodOptions}

	for _, method := range methods {
		t.Run(method, func(t *testing.T) {
			panicHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				panic("method panic")
			})

			req := httptest.NewRequest(method, "/test", nil)
			w := httptest.NewRecorder()

			recoveryHandler := mustRecovery(t, logger)(panicHandler)
			recoveryHandler.ServeHTTP(w, req)

			if w.Code != http.StatusInternalServerError {
				t.Errorf("method %s: expected status 500, got %d", method, w.Code)
			}
		})
	}
}

func TestRecovery_UsesInjectedLogger(t *testing.T) {
	logger := &recordingLogger{}
	panicHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		panic("logger panic")
	})

	handler := mustRecovery(t, logger)(panicHandler)
	req := httptest.NewRequest(http.MethodGet, "/panic", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("expected status 500, got %d", w.Code)
	}

	logger.mu.Lock()
	defer logger.mu.Unlock()
	if logger.errCount != 1 {
		t.Fatalf("expected injected logger to record one error, got %d", logger.errCount)
	}
	if logger.lastMsg != "panic recovered" {
		t.Fatalf("expected panic recovered message, got %q", logger.lastMsg)
	}
	if _, ok := logger.last["panic"]; ok {
		t.Fatalf("raw panic field must not be logged: %v", logger.last["panic"])
	}
	if logger.last["panic_type"] != "string" {
		t.Fatalf("expected sanitized panic type to be logged, got %v", logger.last["panic_type"])
	}
	for _, key := range []string{"method", "path", "status", "duration", "request_id"} {
		if _, ok := logger.last[key]; !ok {
			t.Fatalf("expected %s field to be present", key)
		}
	}
}

func TestRecovery_DoesNotLogRawPanicValue(t *testing.T) {
	logger := &recordingLogger{}
	secret := "token=secret-value"
	handler := mustRecovery(t, logger)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		panic(secret)
	}))

	handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/panic", nil))

	logger.mu.Lock()
	defer logger.mu.Unlock()
	for key, value := range logger.last {
		if value == secret {
			t.Fatalf("field %q leaked raw panic value", key)
		}
	}
	if logger.last["panic_type"] != "string" {
		t.Fatalf("panic_type = %v, want string", logger.last["panic_type"])
	}
}

func TestRecoveryLoggerPanicDoesNotBlockErrorResponse(t *testing.T) {
	logger := &recordingLogger{panicOnRecord: true}
	handler := mustRecovery(t, logger)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		panic("downstream panic")
	}))

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/panic", nil))

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusInternalServerError)
	}
	var response struct {
		Error struct {
			Code     string                 `json:"code"`
			Message  string                 `json:"message"`
			Category contract.ErrorCategory `json:"category"`
			Type     contract.ErrorType     `json:"type,omitempty"`
			Details  map[string]any         `json:"details,omitempty"`
		} `json:"error"`
		RequestID string `json:"request_id,omitempty"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if response.Error.Code != contract.CodeInternalError {
		t.Fatalf("code = %s, want %s", response.Error.Code, contract.CodeInternalError)
	}
}

func TestRecoveryRejectsNilLogger(t *testing.T) {
	if _, err := Middleware(Config{}); !errors.Is(err, ErrNilLogger) {
		t.Fatalf("Middleware error = %v, want %v", err, ErrNilLogger)
	}
}

func TestRecoveryResponseWriterUnwrap(t *testing.T) {
	underlying := httptest.NewRecorder()
	w := &recoveryResponseWriter{BaseWrappedResponseWriter: internaltransport.BaseWrappedResponseWriter{ResponseWriter: underlying}}

	if got := w.Unwrap(); got != underlying {
		t.Fatalf("Unwrap() = %v, want underlying writer", got)
	}
}

// Benchmarks remain to ensure middleware overhead stays bounded.
// --- NEW COVERAGE TESTS ---

// flusherResponseRecorder is an httptest.ResponseRecorder that also implements http.Flusher.
type flusherResponseRecorder struct {
	*httptest.ResponseRecorder
	flushed int
}

func (f *flusherResponseRecorder) Flush() {
	f.flushed++
	f.ResponseRecorder.Flush()
}

// hijackerResponseRecorder is an http.ResponseWriter that also implements http.Hijacker.
type hijackerResponseRecorder struct {
	*httptest.ResponseRecorder
	hijacked bool
}

func (h *hijackerResponseRecorder) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	h.hijacked = true
	server, client := net.Pipe()
	_ = client.Close()
	rw := bufio.NewReadWriter(bufio.NewReader(server), bufio.NewWriter(server))
	return server, rw, nil
}

// TestRecoveryResponseWriterFlush verifies that Flush is forwarded to the underlying
// writer when it implements http.Flusher, and that wrote is set to true first.
func TestRecoveryResponseWriterFlush(t *testing.T) {
	logger := log.NewLogger(log.LoggerConfig{Format: log.LoggerFormatDiscard})
	flushed := false
	handler := mustRecovery(t, logger)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Flush before any write: triggers WriteHeader(200) + flush.
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
			flushed = true
		} else {
			t.Error("expected Flusher interface on recovery writer")
		}
		_, _ = w.Write([]byte("ok"))
	}))

	underlying := &flusherResponseRecorder{ResponseRecorder: httptest.NewRecorder()}
	req := httptest.NewRequest(http.MethodGet, "/flush", nil)
	handler.ServeHTTP(underlying, req)

	if !flushed {
		t.Fatal("handler did not flush")
	}
	if underlying.flushed == 0 {
		t.Fatal("Flush was not forwarded to underlying writer")
	}
	if underlying.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", underlying.Code)
	}
}

// TestRecoveryResponseWriterFlushAfterWrite verifies Flush after a Write does not
// call WriteHeader again.
func TestRecoveryResponseWriterFlushAfterWrite(t *testing.T) {
	logger := log.NewLogger(log.LoggerConfig{Format: log.LoggerFormatDiscard})
	handler := mustRecovery(t, logger)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("data"))
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
	}))

	underlying := &flusherResponseRecorder{ResponseRecorder: httptest.NewRecorder()}
	req := httptest.NewRequest(http.MethodGet, "/flush2", nil)
	handler.ServeHTTP(underlying, req)

	if underlying.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", underlying.Code)
	}
	if underlying.flushed == 0 {
		t.Fatal("Flush was not forwarded to underlying writer after write")
	}
}

// TestRecoveryResponseWriterHijack verifies that Hijack is forwarded through the
// recovery writer and that wrote is set to true afterward.
func TestRecoveryResponseWriterHijack(t *testing.T) {
	logger := log.NewLogger(log.LoggerConfig{Format: log.LoggerFormatDiscard})
	hijacked := false
	handler := mustRecovery(t, logger)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hj, ok := w.(http.Hijacker)
		if !ok {
			t.Error("expected Hijacker interface on recovery writer")
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

	underlying := &hijackerResponseRecorder{ResponseRecorder: httptest.NewRecorder()}
	req := httptest.NewRequest(http.MethodGet, "/hijack", nil)
	handler.ServeHTTP(underlying, req)

	if !hijacked {
		t.Fatal("handler did not successfully hijack")
	}
	if !underlying.hijacked {
		t.Fatal("Hijack was not forwarded to underlying writer")
	}
}

// TestRecoveryResponseWriterHijackNotSupported verifies that Hijack returns an error
// when the underlying writer does not implement http.Hijacker.
func TestRecoveryResponseWriterHijackNotSupported(t *testing.T) {
	w := &recoveryResponseWriter{BaseWrappedResponseWriter: internaltransport.BaseWrappedResponseWriter{ResponseWriter: httptest.NewRecorder()}}
	_, _, err := w.Hijack()
	if err == nil {
		t.Fatal("expected error when underlying writer does not support Hijack")
	}
}

// TestRecoveryResponseWriterWriteHeader_Idempotent verifies that calling WriteHeader
// twice only writes the first status code.
func TestRecoveryResponseWriterWriteHeader_Idempotent(t *testing.T) {
	underlying := httptest.NewRecorder()
	w := &recoveryResponseWriter{BaseWrappedResponseWriter: internaltransport.BaseWrappedResponseWriter{ResponseWriter: underlying}}

	w.WriteHeader(http.StatusCreated)
	w.WriteHeader(http.StatusNotFound) // second call should be ignored

	if underlying.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d", underlying.Code, http.StatusCreated)
	}
}

// TestRecoveryResponseWriterWrite_SetsWrote verifies that Write triggers WriteHeader(200)
// when no status has been set yet.
func TestRecoveryResponseWriterWrite_SetsWrote(t *testing.T) {
	underlying := httptest.NewRecorder()
	w := &recoveryResponseWriter{BaseWrappedResponseWriter: internaltransport.BaseWrappedResponseWriter{ResponseWriter: underlying}}

	n, err := w.Write([]byte("hello"))
	if err != nil {
		t.Fatalf("Write error: %v", err)
	}
	if n != 5 {
		t.Fatalf("Write n = %d, want 5", n)
	}
	if underlying.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", underlying.Code)
	}
	if !w.Written() {
		t.Fatal("wrote flag should be true after Write")
	}
}

// TestPanicTypeNil verifies panicType("unknown") when rec is nil.
// Note: recover() on a nil panic returns nil in Go, but we test panicType directly.
func TestPanicTypeNil(t *testing.T) {
	if got := panicType(nil); got != "unknown" {
		t.Fatalf("panicType(nil) = %q, want %q", got, "unknown")
	}
}

// TestPanicTypeString verifies panicType reports the correct type for a string value.
func TestPanicTypeString(t *testing.T) {
	if got := panicType("boom"); got != "string" {
		t.Fatalf("panicType(string) = %q, want string", got)
	}
}

// TestPanicTypeError verifies panicType reports the correct type for an error value.
func TestPanicTypeError(t *testing.T) {
	if got := panicType(errors.New("err")); got != "*errors.errorString" {
		t.Fatalf("panicType(error) = %q, want *errors.errorString", got)
	}
}

// TestPanicTypeStruct verifies panicType reports the type for an arbitrary struct.
func TestPanicTypeStruct(t *testing.T) {
	type myStruct struct{}
	got := panicType(myStruct{})
	if got != "recovery.myStruct" {
		t.Fatalf("panicType(struct) = %q, want recovery.myStruct", got)
	}
}

// TestRecoveryPanicNil tests that a nil panic is recovered and a 500 is returned.
// Note: panic(nil) in Go returns nil from recover(), so panicType returns "unknown".
func TestRecoveryPanicNil(t *testing.T) {
	logger := &recordingLogger{}
	handler := mustRecovery(t, logger)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		//nolint:staticcheck // SA1012: intentional nil panic to exercise panicType
		panic((*int)(nil)) // typed nil - recover() sees a non-nil value
	}))

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
	logger.mu.Lock()
	defer logger.mu.Unlock()
	if logger.errCount != 1 {
		t.Fatalf("errCount = %d, want 1", logger.errCount)
	}
}

func BenchmarkRecoveryMiddleware_NoPanic(b *testing.B) {
	logger := log.NewLogger(log.LoggerConfig{Format: log.LoggerFormatDiscard})
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	recoveryHandler := mustRecovery(b, logger)(handler)
	req := httptest.NewRequest(http.MethodGet, "/benchmark", nil)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		w := httptest.NewRecorder()
		recoveryHandler.ServeHTTP(w, req)
	}
}

func BenchmarkRecoveryMiddleware_WithPanic(b *testing.B) {
	logger := log.NewLogger(log.LoggerConfig{Format: log.LoggerFormatDiscard})
	panicHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		panic("benchmark panic")
	})

	recoveryHandler := mustRecovery(b, logger)(panicHandler)
	req := httptest.NewRequest(http.MethodGet, "/benchmark", nil)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		w := httptest.NewRecorder()
		recoveryHandler.ServeHTTP(w, req)
	}
}
