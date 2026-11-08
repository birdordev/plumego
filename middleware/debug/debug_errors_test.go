package debug

import (
	"bufio"
	"bytes"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/birdordev/plumego/contract"
)

func assertJSONContentType(t *testing.T, contentType string) {
	t.Helper()

	if !strings.Contains(contentType, "application/json") {
		t.Fatalf("expected json content type, got %q", contentType)
	}
}

func TestDebugErrorsNotFound(t *testing.T) {
	mw := Middleware(Config{NotFoundHint: "/_debug/routes"})
	h := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	}))

	req := httptest.NewRequest(http.MethodGet, "/missing", nil)
	resp := httptest.NewRecorder()
	h.ServeHTTP(resp, req)

	if resp.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", resp.Code)
	}
	assertJSONContentType(t, resp.Header().Get("Content-Type"))

	var payload struct {
		Error struct {
			Code     string                 `json:"code"`
			Message  string                 `json:"message"`
			Category contract.ErrorCategory `json:"category"`
			Type     contract.ErrorType     `json:"type,omitempty"`
			Details  map[string]any         `json:"details,omitempty"`
		} `json:"error"`
		RequestID string `json:"request_id,omitempty"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &payload); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if payload.Error.Code != "not_found" {
		t.Fatalf("expected code not_found, got %q", payload.Error.Code)
	}
	if hint, ok := payload.Error.Details["hint"]; !ok || hint != "/_debug/routes" {
		t.Fatalf("expected hint to be set")
	}
}

func TestDebugErrorsPassThroughJSON(t *testing.T) {
	mw := Middleware(DefaultConfig())
	h := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte(`{"error":"bad"}`))
	}))

	req := httptest.NewRequest(http.MethodPost, "/bad", nil)
	resp := httptest.NewRecorder()
	h.ServeHTTP(resp, req)

	if got := strings.TrimSpace(resp.Body.String()); got != `{"error":"bad"}` {
		t.Fatalf("expected body to pass through, got %q", got)
	}
}

func TestDebugErrorsCaptureLimitPassesThroughOriginalResponse(t *testing.T) {
	mw := Middleware(Config{
		IncludeBody:  true,
		MaxBodyBytes: 4,
	})
	h := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("abcdef"))
		_, _ = w.Write([]byte("gh"))
	}))

	req := httptest.NewRequest(http.MethodGet, "/large-error", nil)
	resp := httptest.NewRecorder()
	h.ServeHTTP(resp, req)

	if resp.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d", resp.Code)
	}
	if got := resp.Body.String(); got != "abcdefgh" {
		t.Fatalf("expected original body to pass through after capture limit, got %q", got)
	}
	if strings.Contains(resp.Body.String(), "response_preview") {
		t.Fatalf("debug replacement should be skipped after capture limit, got %q", resp.Body.String())
	}
}

func TestDebugErrorsIncludeBodyPreviewStillTruncates(t *testing.T) {
	mw := Middleware(Config{
		IncludeBody:  true,
		MaxBodyBytes: 4096,
	})
	h := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(strings.Repeat("x", 2048)))
	}))

	req := httptest.NewRequest(http.MethodGet, "/preview", nil)
	resp := httptest.NewRecorder()
	h.ServeHTTP(resp, req)

	var payload struct {
		Error struct {
			Code     string                 `json:"code"`
			Message  string                 `json:"message"`
			Category contract.ErrorCategory `json:"category"`
			Type     contract.ErrorType     `json:"type,omitempty"`
			Details  map[string]any         `json:"details,omitempty"`
		} `json:"error"`
		RequestID string `json:"request_id,omitempty"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &payload); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	preview, ok := payload.Error.Details["response_preview"].(string)
	if !ok {
		t.Fatalf("expected response preview detail, got %+v", payload.Error.Details)
	}
	if len(preview) != 1027 {
		t.Fatalf("preview length = %d, want 1027", len(preview))
	}
	if !strings.HasSuffix(preview, "...") {
		t.Fatalf("expected truncated preview suffix, got %q", preview[len(preview)-8:])
	}
}

func TestDebugErrorsReplacementDropsStaleContentLength(t *testing.T) {
	mw := Middleware(DefaultConfig())
	h := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.Header().Set("Content-Length", "4")
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("failure"))
	}))

	req := httptest.NewRequest(http.MethodGet, "/stale-length", nil)
	resp := httptest.NewRecorder()
	h.ServeHTTP(resp, req)

	if resp.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", resp.Code, http.StatusInternalServerError)
	}
	if got := resp.Header().Get("Content-Length"); got != "" {
		t.Fatalf("Content-Length = %q, want empty", got)
	}
	assertJSONContentType(t, resp.Header().Get("Content-Type"))
	var payload struct {
		Error struct {
			Code     string                 `json:"code"`
			Message  string                 `json:"message"`
			Category contract.ErrorCategory `json:"category"`
			Type     contract.ErrorType     `json:"type,omitempty"`
			Details  map[string]any         `json:"details,omitempty"`
		} `json:"error"`
		RequestID string `json:"request_id,omitempty"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode replacement body: %v", err)
	}
}

// TestDebugErrorsZeroValueConfig confirms that a zero-value Config
// does not auto-enable IncludeRequest or IncludeQuery (the old piecemeal merge
// silently defaulted those to true).
func TestDebugErrorsZeroValueConfig(t *testing.T) {
	mw := Middleware(Config{})
	h := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	}))

	req := httptest.NewRequest(http.MethodGet, "/missing?foo=bar", nil)
	resp := httptest.NewRecorder()
	h.ServeHTTP(resp, req)

	var payload struct {
		Error struct {
			Code     string                 `json:"code"`
			Message  string                 `json:"message"`
			Category contract.ErrorCategory `json:"category"`
			Type     contract.ErrorType     `json:"type,omitempty"`
			Details  map[string]any         `json:"details,omitempty"`
		} `json:"error"`
		RequestID string `json:"request_id,omitempty"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &payload); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if _, ok := payload.Error.Details["method"]; ok {
		t.Error("expected method not present in details when IncludeRequest is false")
	}
	if _, ok := payload.Error.Details["path"]; ok {
		t.Error("expected path not present in details when IncludeRequest is false")
	}
	if _, ok := payload.Error.Details["query"]; ok {
		t.Error("expected query not present in details when IncludeQuery is false")
	}
}

func TestDebugErrorsSkipUpgrade(t *testing.T) {
	mw := Middleware(DefaultConfig())
	h := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTeapot)
		w.Write([]byte("upgrade"))
	}))

	req := httptest.NewRequest(http.MethodGet, "/ws", nil)
	req.Header.Set("Upgrade", "websocket")
	resp := httptest.NewRecorder()
	h.ServeHTTP(resp, req)

	if got := strings.TrimSpace(resp.Body.String()); got != "upgrade" {
		t.Fatalf("expected body to pass through, got %q", got)
	}
}

func TestDebugErrorsPassThroughResponseDeclaredStream(t *testing.T) {
	mw := Middleware(DefaultConfig())
	h := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte("data: failed\n\n"))
	}))

	req := httptest.NewRequest(http.MethodGet, "/events", nil)
	resp := httptest.NewRecorder()
	h.ServeHTTP(resp, req)

	if resp.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d", resp.Code)
	}
	if got := strings.TrimSpace(resp.Body.String()); got != "data: failed" {
		t.Fatalf("expected stream body to pass through, got %q", got)
	}
	if got := resp.Header().Get("Content-Type"); got != "text/event-stream" {
		t.Fatalf("expected stream content type to pass through, got %q", got)
	}
}

func TestDebugErrorsFlushCommitsPassthrough(t *testing.T) {
	mw := Middleware(DefaultConfig())
	h := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("data: one\n\n"))
		flusher, ok := w.(http.Flusher)
		if !ok {
			t.Fatal("debug recorder did not expose flusher")
		}
		flusher.Flush()
		_, _ = w.Write([]byte("data: two\n\n"))
	}))

	req := httptest.NewRequest(http.MethodGet, "/events", nil)
	resp := &debugFlushWriter{ResponseRecorder: httptest.NewRecorder()}
	h.ServeHTTP(resp, req)

	if resp.flushed == 0 {
		t.Fatal("flush was not forwarded")
	}
	if resp.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", resp.Code, http.StatusInternalServerError)
	}
	if got := resp.Body.String(); got != "data: one\n\ndata: two\n\n" {
		t.Fatalf("body = %q, want original stream body", got)
	}
	if strings.Contains(resp.Body.String(), `"code"`) {
		t.Fatalf("flush path should not replace body with debug JSON: %q", resp.Body.String())
	}
}

func TestDebugErrorsHijackPassesThrough(t *testing.T) {
	mw := Middleware(DefaultConfig())
	h := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hijacker, ok := w.(http.Hijacker)
		if !ok {
			t.Fatal("debug recorder did not expose hijacker")
		}
		conn, _, err := hijacker.Hijack()
		if err != nil {
			t.Fatalf("hijack failed: %v", err)
		}
		if conn != nil {
			_ = conn.Close()
		}
	}))

	req := httptest.NewRequest(http.MethodGet, "/raw", nil)
	writer := &debugHijackWriter{}
	h.ServeHTTP(writer, req)

	if !writer.hijacked {
		t.Fatal("underlying writer was not hijacked")
	}
	if writer.status != 0 {
		t.Fatalf("debug hijack path wrote status %d, want no HTTP status", writer.status)
	}
}

type debugFlushWriter struct {
	*httptest.ResponseRecorder
	flushed int
}

func (w *debugFlushWriter) Flush() {
	w.flushed++
	w.ResponseRecorder.Flush()
}

type debugHijackWriter struct {
	header   http.Header
	status   int
	hijacked bool
}

func (w *debugHijackWriter) Header() http.Header {
	if w.header == nil {
		w.header = make(http.Header)
	}
	return w.header
}

func (w *debugHijackWriter) WriteHeader(status int) {
	w.status = status
}

func (w *debugHijackWriter) Write(p []byte) (int, error) {
	return len(p), nil
}

func (w *debugHijackWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	w.hijacked = true
	server, client := net.Pipe()
	_ = client.Close()
	var rw bytes.Buffer
	return server, bufio.NewReadWriter(bufio.NewReader(&rw), bufio.NewWriter(&rw)), nil
}

// --- NEW COVERAGE TESTS ---

// TestDebugErrorsUnwrap verifies that Unwrap returns the underlying ResponseWriter.
func TestDebugErrorsUnwrap(t *testing.T) {
	underlying := httptest.NewRecorder()
	rec := newDebugErrorRecorder(underlying, defaultMaxBodyBytes)
	if got := rec.Unwrap(); got != underlying {
		t.Fatalf("Unwrap() = %v, want underlying writer", got)
	}
}

// TestDebugErrorsWriteHeader_Idempotent verifies that WriteHeader is a no-op when
// status is already set.
func TestDebugErrorsWriteHeader_Idempotent(t *testing.T) {
	underlying := httptest.NewRecorder()
	rec := newDebugErrorRecorder(underlying, defaultMaxBodyBytes)
	rec.WriteHeader(http.StatusCreated)
	rec.WriteHeader(http.StatusNotFound) // second call should be ignored
	if rec.status != http.StatusCreated {
		t.Fatalf("status = %d, want %d", rec.status, http.StatusCreated)
	}
}

// TestDebugErrorsStatusCode_Zero verifies that a zero status returns 200.
func TestDebugErrorsStatusCode_Zero(t *testing.T) {
	rec := newDebugErrorRecorder(httptest.NewRecorder(), defaultMaxBodyBytes)
	if got := rec.statusCode(); got != http.StatusOK {
		t.Fatalf("statusCode() = %d, want %d", got, http.StatusOK)
	}
}

// TestDebugErrorsWrite_Passthrough verifies the Write passthrough path when
// passthrough is already set.
func TestDebugErrorsWrite_Passthrough(t *testing.T) {
	underlying := httptest.NewRecorder()
	rec := newDebugErrorRecorder(underlying, defaultMaxBodyBytes)
	rec.passthrough = true
	rec.status = http.StatusOK
	n, err := rec.Write([]byte("hello"))
	if err != nil {
		t.Fatalf("Write error = %v", err)
	}
	if n != 5 {
		t.Fatalf("n = %d, want 5", n)
	}
}

// TestDebugErrorsWriteHeader_Passthrough verifies that WriteHeader on a passthrough
// recorder calls flushHeaders on the underlying writer.
func TestDebugErrorsWriteHeader_Passthrough(t *testing.T) {
	underlying := httptest.NewRecorder()
	rec := newDebugErrorRecorder(underlying, defaultMaxBodyBytes)
	rec.passthrough = true
	rec.WriteHeader(http.StatusAccepted)
	if underlying.Code != http.StatusAccepted {
		t.Fatalf("underlying code = %d, want %d", underlying.Code, http.StatusAccepted)
	}
}

// TestDebugErrorsSkipSSE verifies that SSE request (Accept: text/event-stream) is
// skipped by the debug middleware.
func TestDebugErrorsSkipSSE(t *testing.T) {
	mw := Middleware(DefaultConfig())
	h := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte("sse error"))
	}))

	req := httptest.NewRequest(http.MethodGet, "/sse", nil)
	req.Header.Set("Accept", "text/event-stream")
	resp := httptest.NewRecorder()
	h.ServeHTTP(resp, req)

	if resp.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", resp.Code)
	}
	if resp.Body.String() != "sse error" {
		t.Fatalf("expected body to pass through for SSE, got %q", resp.Body.String())
	}
}

// TestDebugErrorsSkipConnect verifies that CONNECT requests are skipped.
func TestDebugErrorsSkipConnect(t *testing.T) {
	mw := Middleware(DefaultConfig())
	h := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte("connect error"))
	}))

	req := httptest.NewRequest(http.MethodConnect, "/proxy", nil)
	resp := httptest.NewRecorder()
	h.ServeHTTP(resp, req)

	if resp.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", resp.Code)
	}
	if resp.Body.String() != "connect error" {
		t.Fatalf("expected body to pass through for CONNECT, got %q", resp.Body.String())
	}
}

// TestDebugErrorsNotFoundRequestDetails verifies IncludeRequest=true adds method/path,
// and IncludeQuery=true adds query params.
func TestDebugErrorsNotFoundRequestDetails(t *testing.T) {
	mw := Middleware(Config{
		IncludeRequest: true,
		IncludeQuery:   true,
		NotFoundHint:   "try /other",
	})
	h := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	}))

	req := httptest.NewRequest(http.MethodGet, "/missing?foo=bar", nil)
	resp := httptest.NewRecorder()
	h.ServeHTTP(resp, req)

	var payload struct {
		Error struct {
			Details map[string]any `json:"details"`
		} `json:"error"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &payload); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if payload.Error.Details["method"] != "GET" {
		t.Errorf("expected method=GET, got %v", payload.Error.Details["method"])
	}
	if payload.Error.Details["path"] != "/missing" {
		t.Errorf("expected path=/missing, got %v", payload.Error.Details["path"])
	}
	if payload.Error.Details["query"] != "foo=bar" {
		t.Errorf("expected query=foo=bar, got %v", payload.Error.Details["query"])
	}
	if payload.Error.Details["hint"] != "try /other" {
		t.Errorf("expected hint, got %v", payload.Error.Details["hint"])
	}
}

// TestDebugErrorType_AllBranches covers all branches in debugErrorType.
func TestDebugErrorType_AllBranches(t *testing.T) {
	tests := []struct {
		status   int
		wantType contract.ErrorType
	}{
		{http.StatusNotFound, contract.TypeNotFound},
		{http.StatusMethodNotAllowed, contract.TypeMethodNotAllowed},
		{http.StatusNotAcceptable, contract.TypeNotAcceptable},
		{http.StatusInternalServerError, contract.TypeInternal},
		{http.StatusBadRequest, contract.TypeBadRequest},
		{http.StatusConflict, contract.TypeBadRequest}, // other 4xx → TypeBadRequest
	}

	for _, tt := range tests {
		t.Run(http.StatusText(tt.status), func(t *testing.T) {
			got := debugErrorType(tt.status)
			if got != tt.wantType {
				t.Fatalf("debugErrorType(%d) = %q, want %q", tt.status, got, tt.wantType)
			}
		})
	}
}

// TestTruncateBody_NoTruncation covers the len(body) <= limit branch in truncateBody.
func TestTruncateBody_NoTruncation(t *testing.T) {
	got := truncateBody([]byte("hello"), 10)
	if got != "hello" {
		t.Fatalf("truncateBody = %q, want hello", got)
	}
}

// TestTruncateBody_ZeroLimit covers the limit <= 0 branch in truncateBody.
func TestTruncateBody_ZeroLimit(t *testing.T) {
	got := truncateBody([]byte("hello"), 0)
	if got != "hello" {
		t.Fatalf("truncateBody with zero limit = %q, want hello", got)
	}
}

// TestDebugErrorsHijackWithBodyCommitsPassthrough covers the Hijack path where
// body.Len() > 0 → commitPassthrough is called first.
func TestDebugErrorsHijackWithBodyCommitsPassthrough(t *testing.T) {
	mw := Middleware(DefaultConfig())
	h := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Write some data first so the recorder's body is non-empty.
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("partial"))
		// Now hijack — body is non-empty so commitPassthrough fires.
		hijacker, ok := w.(http.Hijacker)
		if !ok {
			t.Fatal("debug recorder did not expose hijacker")
		}
		conn, _, err := hijacker.Hijack()
		if err != nil {
			t.Fatalf("hijack failed: %v", err)
		}
		if conn != nil {
			_ = conn.Close()
		}
	}))

	req := httptest.NewRequest(http.MethodGet, "/raw", nil)
	writer := &debugHijackWriter{}
	h.ServeHTTP(writer, req)

	if !writer.hijacked {
		t.Fatal("underlying writer was not hijacked")
	}
}

// TestDebugErrorsHijackWithStatusCommitsPassthrough covers Hijack where status != 0
// but body is empty (the `r.status != 0` branch in Hijack).
func TestDebugErrorsHijackWithStatusCommitsPassthrough(t *testing.T) {
	mw := Middleware(DefaultConfig())
	h := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Set status but no body.
		w.WriteHeader(http.StatusOK)
		hijacker, ok := w.(http.Hijacker)
		if !ok {
			t.Fatal("debug recorder did not expose hijacker")
		}
		conn, _, err := hijacker.Hijack()
		if err != nil {
			t.Fatalf("hijack failed: %v", err)
		}
		if conn != nil {
			_ = conn.Close()
		}
	}))

	req := httptest.NewRequest(http.MethodGet, "/raw", nil)
	writer := &debugHijackWriter{}
	h.ServeHTTP(writer, req)

	if !writer.hijacked {
		t.Fatal("underlying writer was not hijacked")
	}
}

// TestDebugErrorsShouldReplaceError_StreamingContentType covers
// isStreamingContentType returning true in shouldReplaceError.
func TestDebugErrorsShouldReplaceError_StreamingContentType(t *testing.T) {
	mw := Middleware(DefaultConfig())
	h := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/octet-stream")
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte("streaming"))
	}))

	req := httptest.NewRequest(http.MethodGet, "/stream-err", nil)
	resp := httptest.NewRecorder()
	h.ServeHTTP(resp, req)

	if resp.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", resp.Code)
	}
	// Should NOT replace since content type contains "stream".
	if resp.Body.String() != "streaming" {
		t.Fatalf("body = %q, want streaming", resp.Body.String())
	}
}

// TestDebugErrorsCommitPassthrough_EmptyBody covers commitPassthrough when body is empty.
func TestDebugErrorsCommitPassthrough_EmptyBody(t *testing.T) {
	underlying := httptest.NewRecorder()
	rec := newDebugErrorRecorder(underlying, defaultMaxBodyBytes)
	rec.WriteHeader(http.StatusOK)
	rec.commitPassthrough() // body is empty, should just flush headers
	if !rec.passthrough {
		t.Fatal("expected passthrough to be set")
	}
}

// TestDebugErrorsWrite_SetsDefaultStatus verifies that Write with status==0
// sets status to 200 (covers the `r.status == 0` branch in Write).
func TestDebugErrorsWrite_SetsDefaultStatus(t *testing.T) {
	underlying := httptest.NewRecorder()
	rec := newDebugErrorRecorder(underlying, defaultMaxBodyBytes)
	// status is 0 before Write.
	_, _ = rec.Write([]byte("data"))
	if rec.status != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.status)
	}
}

// TestDebugErrorsFlushHeaders_NilDst covers the `r.dst == nil` early return
// in flushHeaders.
func TestDebugErrorsFlushHeaders_NilDst(t *testing.T) {
	rec := &debugErrorRecorder{
		dst:    nil,
		header: make(http.Header),
		status: http.StatusOK,
	}
	rec.flushHeaders() // must not panic
}

// TestDebugErrorsFlushHeaders_Passthrough covers the WriteHeader call inside
// flushHeaders when passthrough=true.
func TestDebugErrorsFlushHeaders_Passthrough(t *testing.T) {
	underlying := httptest.NewRecorder()
	rec := newDebugErrorRecorder(underlying, defaultMaxBodyBytes)
	rec.passthrough = true
	rec.status = http.StatusTeapot
	rec.flushHeaders()
	if underlying.Code != http.StatusTeapot {
		t.Fatalf("underlying code = %d, want 418", underlying.Code)
	}
}

// TestDebugErrorsCommitPassthrough_AlreadyPassthrough covers the early return
// in commitPassthrough when passthrough is already true.
func TestDebugErrorsCommitPassthrough_AlreadyPassthrough(t *testing.T) {
	underlying := httptest.NewRecorder()
	rec := newDebugErrorRecorder(underlying, defaultMaxBodyBytes)
	rec.passthrough = true
	rec.commitPassthrough() // must be a no-op
}

// TestDebugErrorsCommitPassthrough_WithBodyFlushes covers the body flush in
// commitPassthrough when body has data.
func TestDebugErrorsCommitPassthrough_WithBodyFlushes(t *testing.T) {
	underlying := httptest.NewRecorder()
	rec := newDebugErrorRecorder(underlying, defaultMaxBodyBytes)
	rec.status = http.StatusOK
	rec.body.Write([]byte("buffered"))
	rec.commitPassthrough()
	if !rec.passthrough {
		t.Fatal("expected passthrough=true")
	}
	if underlying.Body.String() != "buffered" {
		t.Fatalf("body = %q, want buffered", underlying.Body.String())
	}
}

// TestDebugErrorsShouldSkipDebugErrors_NilRequest covers the nil request path.
func TestDebugErrorsShouldSkipDebugErrors_NilRequest(t *testing.T) {
	if shouldSkipDebugErrors(nil) {
		t.Fatal("nil request should not skip debug errors")
	}
}

// TestDebugErrorsShouldReplaceError_EmptyBody covers the len(body)==0 branch
// in shouldReplaceError (returns true when body is empty and content type is not streaming).
func TestDebugErrorsShouldReplaceError_EmptyBody(t *testing.T) {
	header := make(http.Header)
	header.Set("Content-Type", "text/plain")
	if !shouldReplaceError(header, []byte{}) {
		t.Fatal("expected shouldReplaceError=true for empty body")
	}
}

// TestDebugErrorsShouldReplaceError_NotTextPlain covers the case where the body
// is non-empty and content type is NOT text/plain (returns false).
func TestDebugErrorsShouldReplaceError_NotTextPlain(t *testing.T) {
	header := make(http.Header)
	header.Set("Content-Type", "application/json")
	if shouldReplaceError(header, []byte(`{"error":"x"}`)) {
		t.Fatal("expected shouldReplaceError=false for non-text/plain body")
	}
}

// TestDebugErrorsWrite_BodyFlushedOnPassthroughTransition covers the branch in
// Write (line 184-190) where body.Len() > 0 when transitioning to passthrough.
// We need: small first write fits in buffer, second write pushes body+new > maxBytes.
func TestDebugErrorsWrite_BodyFlushedOnPassthroughTransition(t *testing.T) {
	mw := Middleware(Config{
		MaxBodyBytes: 4,
	})
	h := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(http.StatusInternalServerError)
		// First write: 3 bytes → fits in buffer (3 ≤ 4).
		_, _ = w.Write([]byte("abc"))
		// Second write: 3 bytes → body.Len()=3, 3+3=6 > 4 → triggers passthrough.
		// body is non-empty (3 bytes) so the `r.body.Len() > 0` branch fires.
		_, _ = w.Write([]byte("def"))
	}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	resp := httptest.NewRecorder()
	h.ServeHTTP(resp, req)

	if resp.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", resp.Code)
	}
	// Body should have the original content (passthrough after buffer flush).
	if got := resp.Body.String(); got != "abcdef" {
		t.Fatalf("body = %q, want abcdef", got)
	}
}
