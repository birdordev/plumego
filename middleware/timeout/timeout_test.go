package timeout

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/birdordev/plumego/contract"
	"github.com/birdordev/plumego/log"
	"github.com/birdordev/plumego/middleware/recovery"
)

func applyMiddleware(h http.Handler, mw func(http.Handler) http.Handler) http.Handler {
	return mw(h)
}

func recoveryMiddleware(t *testing.T) func(http.Handler) http.Handler {
	t.Helper()
	mw, err := recovery.Middleware(recovery.Config{
		Logger: log.NewLogger(log.LoggerConfig{Format: log.LoggerFormatDiscard}),
	})
	if err != nil {
		t.Fatalf("recovery middleware: %v", err)
	}
	return mw
}

func TestTimeoutMiddleware_TimesOut(t *testing.T) {
	canceled := make(chan struct{})

	handler := func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-time.After(200 * time.Millisecond):
			w.WriteHeader(http.StatusOK)
		case <-r.Context().Done():
			close(canceled)
		}
	}

	wrapped := applyMiddleware(http.HandlerFunc(handler), Middleware(Config{Timeout: 20 * time.Millisecond}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rr := httptest.NewRecorder()

	wrapped.ServeHTTP(rr, req)

	var resp struct {
		Error struct {
			Code     string                 `json:"code"`
			Message  string                 `json:"message"`
			Category contract.ErrorCategory `json:"category"`
			Type     contract.ErrorType     `json:"type,omitempty"`
			Details  map[string]any         `json:"details,omitempty"`
		} `json:"error"`
		RequestID string `json:"request_id,omitempty"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}

	if rr.Code != http.StatusGatewayTimeout {
		t.Fatalf("expected status %d, got %d", http.StatusGatewayTimeout, rr.Code)
	}

	if resp.Error.Code != contract.CodeTimeout || resp.Error.Category != contract.CategoryTimeout {
		t.Fatalf("unexpected response payload: %+v", resp)
	}

	select {
	case <-canceled:
	case <-time.After(100 * time.Millisecond):
		t.Fatalf("handler context was not canceled")
	}
}

func TestTimeoutMiddleware_DoesNotForceStopIgnoredContext(t *testing.T) {
	completed := make(chan struct{})

	handler := func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(40 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
		close(completed)
	}

	wrapped := applyMiddleware(http.HandlerFunc(handler), Middleware(Config{Timeout: 10 * time.Millisecond}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rr := httptest.NewRecorder()

	wrapped.ServeHTTP(rr, req)

	if rr.Code != http.StatusGatewayTimeout {
		t.Fatalf("expected status %d, got %d", http.StatusGatewayTimeout, rr.Code)
	}

	select {
	case <-completed:
	case <-time.After(100 * time.Millisecond):
		t.Fatalf("handler did not complete after ignoring context cancellation")
	}
}

func TestTimeoutMiddleware_PostTimeoutWriteReturnsContextError(t *testing.T) {
	type writeResult struct {
		n   int
		err error
	}
	result := make(chan writeResult, 1)

	handler := func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(40 * time.Millisecond)
		n, err := w.Write([]byte("late"))
		result <- writeResult{n: n, err: err}
	}

	wrapped := applyMiddleware(http.HandlerFunc(handler), Middleware(Config{Timeout: 10 * time.Millisecond}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rr := httptest.NewRecorder()

	wrapped.ServeHTTP(rr, req)

	if rr.Code != http.StatusGatewayTimeout {
		t.Fatalf("expected status %d, got %d", http.StatusGatewayTimeout, rr.Code)
	}

	select {
	case got := <-result:
		if got.n != 0 {
			t.Fatalf("post-timeout write bytes = %d, want 0", got.n)
		}
		if !errors.Is(got.err, context.DeadlineExceeded) {
			t.Fatalf("post-timeout write error = %v, want context deadline exceeded", got.err)
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatalf("handler did not attempt post-timeout write")
	}
}

func TestTimeoutMiddleware_PassThrough(t *testing.T) {
	handler := func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Test", "ok")
		w.WriteHeader(http.StatusCreated)
		w.Write([]byte("done"))
	}

	wrapped := applyMiddleware(http.HandlerFunc(handler), Middleware(Config{Timeout: 500 * time.Millisecond}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rr := httptest.NewRecorder()

	wrapped.ServeHTTP(rr, req)

	if rr.Code != http.StatusCreated {
		t.Fatalf("expected status %d, got %d", http.StatusCreated, rr.Code)
	}

	if rr.Body.String() != "done" {
		t.Fatalf("unexpected body: %q", rr.Body.String())
	}

	if rr.Header().Get("X-Test") != "ok" {
		t.Fatalf("header not propagated")
	}
}

func TestTimeoutMiddleware_RepanicsWorkerPanic(t *testing.T) {
	wrapped := applyMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		panic("worker panic")
	}), Middleware(Config{Timeout: 500 * time.Millisecond}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rr := httptest.NewRecorder()

	defer func() {
		if rec := recover(); rec != "worker panic" {
			t.Fatalf("panic = %v, want worker panic", rec)
		}
	}()

	wrapped.ServeHTTP(rr, req)
}

func TestTimeoutMiddleware_OuterRecoveryHandlesWorkerPanic(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		panic("worker panic")
	})
	wrapped := recoveryMiddleware(t)(
		Middleware(Config{Timeout: 500 * time.Millisecond})(handler),
	)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rr := httptest.NewRecorder()
	wrapped.ServeHTTP(rr, req)

	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("expected status %d, got %d", http.StatusInternalServerError, rr.Code)
	}
	var resp struct {
		Error struct {
			Code     string                 `json:"code"`
			Message  string                 `json:"message"`
			Category contract.ErrorCategory `json:"category"`
			Type     contract.ErrorType     `json:"type,omitempty"`
			Details  map[string]any         `json:"details,omitempty"`
		} `json:"error"`
		RequestID string `json:"request_id,omitempty"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}
	if resp.Error.Code != contract.CodeInternalError || resp.Error.Category != contract.CategoryServer {
		t.Fatalf("unexpected response payload: %+v", resp)
	}
}

func TestTimeoutMiddleware_OuterRecoveryHandlesWorkerPanicAfterBufferedWrite(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte("partial"))
		panic("worker panic")
	})
	wrapped := recoveryMiddleware(t)(
		Middleware(Config{Timeout: 500 * time.Millisecond})(handler),
	)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rr := httptest.NewRecorder()
	wrapped.ServeHTTP(rr, req)

	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("expected status %d, got %d", http.StatusInternalServerError, rr.Code)
	}
	if strings.Contains(rr.Body.String(), "partial") {
		t.Fatalf("buffered partial response leaked into recovery response: %q", rr.Body.String())
	}
}

func TestTimeoutMiddleware_PostTimeoutPanicCallsHook(t *testing.T) {
	type panicReport struct {
		path      string
		recovered any
	}
	reports := make(chan panicReport, 1)

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(40 * time.Millisecond)
		panic("late panic")
	})
	wrapped := Middleware(Config{
		Timeout: 10 * time.Millisecond,
		OnPanic: func(r *http.Request, recovered any) {
			reports <- panicReport{path: r.URL.Path, recovered: recovered}
		},
	})(handler)

	req := httptest.NewRequest(http.MethodGet, "/late", nil)
	rr := httptest.NewRecorder()
	wrapped.ServeHTTP(rr, req)

	if rr.Code != http.StatusGatewayTimeout {
		t.Fatalf("expected status %d, got %d", http.StatusGatewayTimeout, rr.Code)
	}

	select {
	case report := <-reports:
		if report.path != "/late" {
			t.Fatalf("OnPanic request path = %q, want /late", report.path)
		}
		if report.recovered != "late panic" {
			t.Fatalf("OnPanic recovered = %v, want late panic", report.recovered)
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("post-timeout panic was not reported")
	}
}

func TestTimeoutMiddleware_PostTimeoutPanicHookPanicIsRecovered(t *testing.T) {
	called := make(chan struct{}, 1)
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(40 * time.Millisecond)
		panic("late panic")
	})
	wrapped := Middleware(Config{
		Timeout: 10 * time.Millisecond,
		OnPanic: func(r *http.Request, recovered any) {
			called <- struct{}{}
			panic("hook panic")
		},
	})(handler)

	req := httptest.NewRequest(http.MethodGet, "/late", nil)
	rr := httptest.NewRecorder()
	wrapped.ServeHTTP(rr, req)

	if rr.Code != http.StatusGatewayTimeout {
		t.Fatalf("expected status %d, got %d", http.StatusGatewayTimeout, rr.Code)
	}
	select {
	case <-called:
	case <-time.After(100 * time.Millisecond):
		t.Fatal("post-timeout panic hook was not called")
	}
}

func TestTimeoutMiddleware_DisabledWhenTimeoutNonPositive(t *testing.T) {
	handler := func(w http.ResponseWriter, r *http.Request) {
		if err := r.Context().Err(); err != nil {
			t.Fatalf("unexpected canceled request context: %v", err)
		}
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte("disabled"))
	}

	wrapped := applyMiddleware(http.HandlerFunc(handler), Middleware(Config{}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rr := httptest.NewRecorder()

	wrapped.ServeHTTP(rr, req)

	if rr.Code != http.StatusAccepted {
		t.Fatalf("expected status %d, got %d", http.StatusAccepted, rr.Code)
	}
	if rr.Body.String() != "disabled" {
		t.Fatalf("unexpected body: %q", rr.Body.String())
	}
}

func TestTimeoutMiddleware_BufferLimit(t *testing.T) {
	handler := func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("toolarge"))
	}

	wrapped := applyMiddleware(http.HandlerFunc(handler), Middleware(Config{
		Timeout:        500 * time.Millisecond,
		MaxBufferBytes: 4,
	}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rr := httptest.NewRecorder()

	wrapped.ServeHTTP(rr, req)

	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("expected status %d, got %d", http.StatusInternalServerError, rr.Code)
	}

	var resp struct {
		Error struct {
			Code     string                 `json:"code"`
			Message  string                 `json:"message"`
			Category contract.ErrorCategory `json:"category"`
			Type     contract.ErrorType     `json:"type,omitempty"`
			Details  map[string]any         `json:"details,omitempty"`
		} `json:"error"`
		RequestID string `json:"request_id,omitempty"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}
	if resp.Error.Message != "response exceeded buffer limit" {
		t.Fatalf("unexpected body: %q", rr.Body.String())
	}
	if resp.Error.Code != contract.CodeInternalError || resp.Error.Category != contract.CategoryServer {
		t.Fatalf("unexpected response payload: %+v", resp)
	}
}

func TestTimeoutMiddleware_LargeResponseExceedsMaxReplayBytes(t *testing.T) {
	// Large responses abandon buffering and are converted into a structured
	// server error because they cannot be replayed safely.
	handler := func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		largeData := make([]byte, 1<<20)
		for i := range largeData {
			largeData[i] = byte('A' + (i % 26))
		}
		w.Write(largeData)
	}

	wrapped := applyMiddleware(http.HandlerFunc(handler), Middleware(Config{
		Timeout:        500 * time.Millisecond,
		MaxBufferBytes: 10 << 20,
		MaxReplayBytes: 512 << 10, // 512KB
	}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rr := httptest.NewRecorder()

	wrapped.ServeHTTP(rr, req)

	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("expected status %d for bypassed large response, got %d", http.StatusInternalServerError, rr.Code)
	}

	var resp struct {
		Error struct {
			Code     string                 `json:"code"`
			Message  string                 `json:"message"`
			Category contract.ErrorCategory `json:"category"`
			Type     contract.ErrorType     `json:"type,omitempty"`
			Details  map[string]any         `json:"details,omitempty"`
		} `json:"error"`
		RequestID string `json:"request_id,omitempty"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}
	if resp.Error.Message != "response too large for timeout buffering" {
		t.Fatalf("unexpected body: %q", rr.Body.String())
	}
	if resp.Error.Code != contract.CodeInternalError || resp.Error.Category != contract.CategoryServer {
		t.Fatalf("unexpected response payload: %+v", resp)
	}
}

func TestTimeoutMiddleware_SmallResponseBuffered(t *testing.T) {
	// Test that small responses are still buffered normally
	handler := func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Test", "ok")
		w.WriteHeader(http.StatusCreated)
		w.Write([]byte("small"))
	}

	wrapped := applyMiddleware(http.HandlerFunc(handler), Middleware(Config{
		Timeout:        500 * time.Millisecond,
		MaxBufferBytes: 10 << 20,
		MaxReplayBytes: 512 << 10, // 512KB
	}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rr := httptest.NewRecorder()

	wrapped.ServeHTTP(rr, req)

	if rr.Code != http.StatusCreated {
		t.Fatalf("expected status %d, got %d", http.StatusCreated, rr.Code)
	}

	if rr.Body.String() != "small" {
		t.Fatalf("unexpected body: %q", rr.Body.String())
	}

	if rr.Header().Get("X-Test") != "ok" {
		t.Fatalf("header not propagated")
	}
}

func TestTimeoutMiddleware_MaxReplayBytes(t *testing.T) {
	// Test response exactly at threshold boundary
	handler := func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		// 512KB exactly
		data := make([]byte, 512<<10)
		w.Write(data)
	}

	wrapped := applyMiddleware(http.HandlerFunc(handler), Middleware(Config{
		Timeout:        500 * time.Millisecond,
		MaxBufferBytes: 10 << 20,
		MaxReplayBytes: 512 << 10, // 512KB
	}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rr := httptest.NewRecorder()

	wrapped.ServeHTTP(rr, req)

	// Should be buffered (not exceed threshold)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d", http.StatusOK, rr.Code)
	}

	if rr.Body.Len() != 512<<10 {
		t.Fatalf("expected body length %d, got %d", 512<<10, rr.Body.Len())
	}
}
