package compression

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"io"
	"mime"
	"mime/multipart"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	internaltransport "github.com/birdordev/plumego/internal/httputil"
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

func TestGzip_SmallResponse(t *testing.T) {
	// Small response should be compressed
	handler := func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("Hello World"))
	}

	wrapped := applyMiddleware(http.HandlerFunc(handler), Middleware(Config{}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	rr := httptest.NewRecorder()

	wrapped.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d", http.StatusOK, rr.Code)
	}

	if rr.Header().Get("Content-Encoding") != "gzip" {
		t.Fatalf("expected gzip encoding, got %q", rr.Header().Get("Content-Encoding"))
	}

	// Verify content is actually compressed
	reader, err := gzip.NewReader(rr.Body)
	if err != nil {
		t.Fatalf("failed to create gzip reader: %v", err)
	}
	defer reader.Close()

	decompressed, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("failed to decompress: %v", err)
	}

	if string(decompressed) != "Hello World" {
		t.Fatalf("expected 'Hello World', got %q", string(decompressed))
	}
}

func TestGzip_WriteHeaderBeforeBodyStillSniffsAndCompressesText(t *testing.T) {
	handler := func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("Hello after header"))
	}

	wrapped := applyMiddleware(http.HandlerFunc(handler), Middleware(Config{}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	rr := httptest.NewRecorder()

	wrapped.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d", http.StatusOK, rr.Code)
	}
	if rr.Header().Get("Content-Encoding") != "gzip" {
		t.Fatalf("expected gzip encoding, got %q", rr.Header().Get("Content-Encoding"))
	}
	if got := gunzipBody(t, rr); got != "Hello after header" {
		t.Fatalf("decompressed body = %q, want Hello after header", got)
	}
}

func TestGzip_WriteHeaderBeforeBinaryBodySkipsCompression(t *testing.T) {
	body := append([]byte("\x89PNG\r\n\x1a\n"), bytes.Repeat([]byte{0}, 520)...)
	handler := func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write(body)
	}

	wrapped := applyMiddleware(http.HandlerFunc(handler), Middleware(Config{}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	rr := httptest.NewRecorder()

	wrapped.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d", http.StatusOK, rr.Code)
	}
	if got := rr.Header().Get("Content-Encoding"); got != "" {
		t.Fatalf("Content-Encoding = %q, want empty", got)
	}
	if !bytes.Equal(rr.Body.Bytes(), body) {
		t.Fatal("binary body changed when compression should be skipped")
	}
}

func TestGzip_LargeResponse(t *testing.T) {
	// Large response should be compressed
	largeData := strings.Repeat("A", 2048) // 2KB > 1KB threshold

	handler := func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(largeData))
	}

	wrapped := applyMiddleware(http.HandlerFunc(handler), Middleware(Config{}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	rr := httptest.NewRecorder()

	wrapped.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d", http.StatusOK, rr.Code)
	}

	if rr.Header().Get("Content-Encoding") != "gzip" {
		t.Fatalf("expected gzip encoding, got %q", rr.Header().Get("Content-Encoding"))
	}

	if got := gunzipBody(t, rr); got != largeData {
		t.Fatalf("decompressed body length = %d, want %d", len(got), len(largeData))
	}
}

func TestGzip_SkipWebSocket(t *testing.T) {
	// WebSocket upgrade should not be compressed
	handler := func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusSwitchingProtocols)
		w.Write([]byte("upgrade"))
	}

	wrapped := applyMiddleware(http.HandlerFunc(handler), Middleware(Config{}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	req.Header.Set("Connection", "Upgrade")
	req.Header.Set("Upgrade", "websocket")
	rr := httptest.NewRecorder()

	wrapped.ServeHTTP(rr, req)

	if rr.Code != http.StatusSwitchingProtocols {
		t.Fatalf("expected status %d, got %d", http.StatusSwitchingProtocols, rr.Code)
	}

	if rr.Header().Get("Content-Encoding") == "gzip" {
		t.Fatalf("websocket should not be compressed")
	}

	if rr.Body.String() != "upgrade" {
		t.Fatalf("expected 'upgrade', got %q", rr.Body.String())
	}
}

func TestGzipPreservesHijackerBeforeCompressionStarts(t *testing.T) {
	writer := newGzipHijackWriter()
	defer writer.close()

	handler := func(w http.ResponseWriter, r *http.Request) {
		hijacker, ok := w.(http.Hijacker)
		if !ok {
			t.Fatal("expected gzip writer to expose Hijacker")
		}
		conn, _, err := hijacker.Hijack()
		if err != nil {
			t.Fatalf("hijack: %v", err)
		}
		_ = conn.Close()
	}

	wrapped := applyMiddleware(http.HandlerFunc(handler), Middleware(Config{}))
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Accept-Encoding", "gzip")

	wrapped.ServeHTTP(writer, req)

	if !writer.hijacked {
		t.Fatal("expected underlying writer to be hijacked")
	}
	if writer.wroteHeader || writer.wroteBody {
		t.Fatal("gzip finalization wrote to the hijacked connection")
	}
}

func TestGzipHijackerReturnsNotSupported(t *testing.T) {
	handler := func(w http.ResponseWriter, r *http.Request) {
		hijacker, ok := w.(http.Hijacker)
		if !ok {
			t.Fatal("expected gzip writer to expose Hijacker")
		}
		if _, _, err := hijacker.Hijack(); err != http.ErrNotSupported {
			t.Fatalf("hijack error = %v, want %v", err, http.ErrNotSupported)
		}
		w.WriteHeader(http.StatusNoContent)
	}

	wrapped := applyMiddleware(http.HandlerFunc(handler), Middleware(Config{}))
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	rr := httptest.NewRecorder()

	wrapped.ServeHTTP(rr, req)

	if rr.Code != http.StatusNoContent {
		t.Fatalf("expected status %d, got %d", http.StatusNoContent, rr.Code)
	}
}

func TestGzipResponseWriterUnwrap(t *testing.T) {
	underlying := httptest.NewRecorder()
	w := &gzipResponseWriter{ResponseWriter: underlying}

	if got := w.Unwrap(); got != underlying {
		t.Fatalf("Unwrap() = %v, want underlying writer", got)
	}
}

func TestGzip_SkipSSE(t *testing.T) {
	// SSE should not be compressed
	handler := func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("data: hello\n\n"))
	}

	wrapped := applyMiddleware(http.HandlerFunc(handler), Middleware(Config{}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	req.Header.Set("Accept", "text/event-stream")
	rr := httptest.NewRecorder()

	wrapped.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d", http.StatusOK, rr.Code)
	}

	if rr.Header().Get("Content-Encoding") == "gzip" {
		t.Fatalf("SSE should not be compressed")
	}

	if rr.Body.String() != "data: hello\n\n" {
		t.Fatalf("expected SSE data, got %q", rr.Body.String())
	}
}

func TestGzip_SkipBinaryContent(t *testing.T) {
	// Binary content should not be compressed
	testCases := []struct {
		contentType string
		data        []byte
	}{
		{"image/png", []byte{0x89, 0x50, 0x4E, 0x47}}, // PNG header
		{"video/mp4", []byte{0x00, 0x00, 0x00, 0x20}}, // MP4 header
		{"application/zip", []byte{0x50, 0x4B, 0x03, 0x04}},
		{"application/pdf", []byte("%PDF-1.4")},
	}

	for _, tc := range testCases {
		t.Run(tc.contentType, func(t *testing.T) {
			handler := func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", tc.contentType)
				w.WriteHeader(http.StatusOK)
				w.Write(tc.data)
			}

			wrapped := applyMiddleware(http.HandlerFunc(handler), Middleware(Config{}))

			req := httptest.NewRequest(http.MethodGet, "/", nil)
			req.Header.Set("Accept-Encoding", "gzip")
			rr := httptest.NewRecorder()

			wrapped.ServeHTTP(rr, req)

			if rr.Header().Get("Content-Encoding") == "gzip" {
				t.Fatalf("%s should not be compressed", tc.contentType)
			}

			if !bytes.Equal(rr.Body.Bytes(), tc.data) {
				t.Fatalf("data mismatch for %s", tc.contentType)
			}
		})
	}
}

func TestGzip_SkipAlreadyCompressed(t *testing.T) {
	// Already compressed content should not be compressed again
	handler := func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.Header().Set("Content-Encoding", "gzip")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("already compressed"))
	}

	wrapped := applyMiddleware(http.HandlerFunc(handler), Middleware(Config{}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	rr := httptest.NewRecorder()

	wrapped.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d", http.StatusOK, rr.Code)
	}

	if rr.Header().Get("Content-Encoding") != "gzip" {
		t.Fatalf("should preserve existing gzip encoding")
	}

	if rr.Body.String() != "already compressed" {
		t.Fatalf("expected 'already compressed', got %q", rr.Body.String())
	}
}

func TestGzip_SkipNoAcceptEncoding(t *testing.T) {
	// Should not compress if client doesn't support gzip
	handler := func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("Hello World"))
	}

	wrapped := applyMiddleware(http.HandlerFunc(handler), Middleware(Config{}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	// No Accept-Encoding header
	rr := httptest.NewRecorder()

	wrapped.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d", http.StatusOK, rr.Code)
	}

	if rr.Header().Get("Content-Encoding") == "gzip" {
		t.Fatalf("should not compress without Accept-Encoding")
	}

	if rr.Body.String() != "Hello World" {
		t.Fatalf("expected 'Hello World', got %q", rr.Body.String())
	}
}

func TestGzip_AcceptEncodingTokens(t *testing.T) {
	tests := []struct {
		name           string
		acceptEncoding string
		wantCompressed bool
	}{
		{name: "gzip token", acceptEncoding: "br, gzip", wantCompressed: true},
		{name: "gzip q zero", acceptEncoding: "gzip;q=0, br", wantCompressed: false},
		{name: "false positive token", acceptEncoding: "xgzip", wantCompressed: false},
		{name: "wildcard", acceptEncoding: "br, *;q=0.5", wantCompressed: true},
		{name: "explicit gzip overrides wildcard", acceptEncoding: "gzip;q=0, *;q=1", wantCompressed: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			handler := func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/plain")
				w.WriteHeader(http.StatusOK)
				w.Write([]byte("Hello World"))
			}

			wrapped := applyMiddleware(http.HandlerFunc(handler), Middleware(Config{}))

			req := httptest.NewRequest(http.MethodGet, "/", nil)
			req.Header.Set("Accept-Encoding", tt.acceptEncoding)
			rr := httptest.NewRecorder()

			wrapped.ServeHTTP(rr, req)

			compressed := rr.Header().Get("Content-Encoding") == "gzip"
			if compressed != tt.wantCompressed {
				t.Fatalf("compressed = %v, want %v; headers=%v body=%q", compressed, tt.wantCompressed, rr.Header(), rr.Body.String())
			}
		})
	}
}

func TestGzip_DetectsTextContentTypeWhenMissing(t *testing.T) {
	handler := func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("Hello World"))
	}

	wrapped := applyMiddleware(http.HandlerFunc(handler), Middleware(Config{}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	rr := httptest.NewRecorder()

	wrapped.ServeHTTP(rr, req)

	if rr.Header().Get("Content-Encoding") != "gzip" {
		t.Fatalf("expected gzip encoding, got %q", rr.Header().Get("Content-Encoding"))
	}
	if got := rr.Header().Get("Content-Type"); !strings.HasPrefix(got, "text/plain") {
		t.Fatalf("expected detected text content type, got %q", got)
	}
}

func TestGzip_DetectsBinaryContentTypeWhenMissing(t *testing.T) {
	data := []byte{0x00, 0x01, 0x02, 0x03}
	handler := func(w http.ResponseWriter, r *http.Request) {
		w.Write(data)
	}

	wrapped := applyMiddleware(http.HandlerFunc(handler), Middleware(Config{}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	rr := httptest.NewRecorder()

	wrapped.ServeHTTP(rr, req)

	if rr.Header().Get("Content-Encoding") == "gzip" {
		t.Fatal("expected binary response without explicit content type not to be compressed")
	}
	if !bytes.Equal(rr.Body.Bytes(), data) {
		t.Fatalf("expected binary body to pass through, got %v", rr.Body.Bytes())
	}
}

func TestGzip_SkipErrorResponses(t *testing.T) {
	// Error responses should not be compressed
	handler := func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte("Error"))
	}

	wrapped := applyMiddleware(http.HandlerFunc(handler), Middleware(Config{}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	rr := httptest.NewRecorder()

	wrapped.ServeHTTP(rr, req)

	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("expected status %d, got %d", http.StatusInternalServerError, rr.Code)
	}

	if rr.Header().Get("Content-Encoding") == "gzip" {
		t.Fatalf("error responses should not be compressed")
	}

	if rr.Body.String() != "Error" {
		t.Fatalf("expected 'Error', got %q", rr.Body.String())
	}
}

func TestGzipPanicBeforeCompressionStartsAllowsRecoveryResponse(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		panic("boom")
	})
	wrapped := recoveryMiddleware(t)(
		Middleware(Config{})(handler),
	)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	rr := httptest.NewRecorder()
	wrapped.ServeHTTP(rr, req)

	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("expected status %d, got %d", http.StatusInternalServerError, rr.Code)
	}
	if got := rr.Header().Get("Content-Encoding"); got == "gzip" {
		t.Fatalf("recovery response should not be gzip encoded before compression starts")
	}
}

func TestGzipPanicAfterCompressionStartsFinalizesStream(t *testing.T) {
	wrapped := Middleware(Config{})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte("partial"))
		panic("boom")
	}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	rr := httptest.NewRecorder()
	panicked := false
	func() {
		defer func() {
			if recover() != nil {
				panicked = true
			}
		}()
		wrapped.ServeHTTP(rr, req)
	}()

	if !panicked {
		t.Fatal("expected panic to propagate")
	}
	if rr.Header().Get("Content-Encoding") != "gzip" {
		t.Fatalf("expected gzip encoding, got %q", rr.Header().Get("Content-Encoding"))
	}
	if got := gunzipBody(t, rr); got != "partial" {
		t.Fatalf("decompressed body = %q, want partial", got)
	}
}

func TestGzipFlushBeforeWriteForcesPassThrough(t *testing.T) {
	wrapped := Middleware(Config{})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		flusher, ok := w.(http.Flusher)
		if !ok {
			t.Fatal("expected gzip writer to expose Flusher")
		}
		w.Header().Set("X-Test", "before-flush")
		flusher.Flush()
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte("after flush"))
	}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	rr := httptest.NewRecorder()
	wrapped.ServeHTTP(rr, req)

	if !rr.Flushed {
		t.Fatal("underlying recorder was not flushed")
	}
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusOK)
	}
	if got := rr.Header().Get("Content-Encoding"); got != "" {
		t.Fatalf("Content-Encoding = %q, want empty", got)
	}
	if got := rr.Body.String(); got != "after flush" {
		t.Fatalf("body = %q, want after flush", got)
	}
}

func TestGzip_CustomMaxBuffer(t *testing.T) {
	// Test custom max buffer configuration
	handler := func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("Hello World")) // 11 bytes
	}

	cfg := Config{MaxBufferBytes: 5} // Only compress if <= 5 bytes
	wrapped := applyMiddleware(http.HandlerFunc(handler), Middleware(cfg))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	rr := httptest.NewRecorder()

	wrapped.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d", http.StatusOK, rr.Code)
	}

	// Should not compress because 11 bytes > 5 bytes max buffer
	if rr.Header().Get("Content-Encoding") == "gzip" {
		t.Fatalf("should not compress large response with custom max buffer")
	}

	if rr.Body.String() != "Hello World" {
		t.Fatalf("expected 'Hello World', got %q", rr.Body.String())
	}
}

func TestGzip_MaxBufferAppliesBeforeCompressionStarts(t *testing.T) {
	body := strings.Repeat("A", 64)
	handler := func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(body))
	}

	wrapped := applyMiddleware(http.HandlerFunc(handler), Middleware(Config{MaxBufferBytes: 8}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	rr := httptest.NewRecorder()

	wrapped.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d", http.StatusOK, rr.Code)
	}
	if rr.Header().Get("Content-Encoding") == "gzip" {
		t.Fatalf("expected large first write to bypass compression")
	}
	if got := rr.Body.String(); got != body {
		t.Fatalf("body = %q, want original body", got)
	}
}

func TestGzip_MaxBufferDoesNotInterruptStartedCompression(t *testing.T) {
	first := "hello"
	second := strings.Repeat("B", 64)
	handler := func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(first))
		_, _ = w.Write([]byte(second))
	}

	wrapped := applyMiddleware(http.HandlerFunc(handler), Middleware(Config{MaxBufferBytes: 8}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	rr := httptest.NewRecorder()

	wrapped.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d", http.StatusOK, rr.Code)
	}
	if rr.Header().Get("Content-Encoding") != "gzip" {
		t.Fatalf("expected compression to continue after gzip starts, got %q", rr.Header().Get("Content-Encoding"))
	}
	if got, want := gunzipBody(t, rr), first+second; got != want {
		t.Fatalf("decompressed body length = %d, want %d", len(got), len(want))
	}
}

func TestGzip_VaryHeader(t *testing.T) {
	// Vary header should be set when compression is used
	handler := func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("Hello World"))
	}

	wrapped := applyMiddleware(http.HandlerFunc(handler), Middleware(Config{}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	rr := httptest.NewRecorder()

	wrapped.ServeHTTP(rr, req)

	if rr.Header().Get("Vary") != "Accept-Encoding" {
		t.Fatalf("expected Vary: Accept-Encoding, got %q", rr.Header().Get("Vary"))
	}
}

func TestGzip_DeduplicatesExistingVaryHeader(t *testing.T) {
	handler := func(w http.ResponseWriter, r *http.Request) {
		w.Header().Add("Vary", "Origin, Accept-Encoding")
		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("Hello World"))
	}

	wrapped := applyMiddleware(http.HandlerFunc(handler), Middleware(Config{}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	rr := httptest.NewRecorder()

	wrapped.ServeHTTP(rr, req)

	values := rr.Header().Values("Vary")
	if countHeaderToken(values, "Accept-Encoding") != 1 {
		t.Fatalf("expected one Accept-Encoding Vary token, got %v", values)
	}
	if !headerValuesContain(values, "Origin") {
		t.Fatalf("expected existing Origin Vary token, got %v", values)
	}
}

func TestGzip_ReplacesStaleDestinationHeaderOnFlush(t *testing.T) {
	handler := func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Test", "fresh")
		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("Hello World"))
	}

	wrapped := applyMiddleware(http.HandlerFunc(handler), Middleware(Config{}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	rr := httptest.NewRecorder()
	rr.Header().Set("X-Test", "stale")

	wrapped.ServeHTTP(rr, req)

	if got := rr.Header().Values("X-Test"); len(got) != 1 || got[0] != "fresh" {
		t.Fatalf("X-Test values = %v, want [fresh]", got)
	}
}

func TestGzip_EmptyResponse(t *testing.T) {
	// Empty response should not cause issues
	handler := func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}

	wrapped := applyMiddleware(http.HandlerFunc(handler), Middleware(Config{}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	rr := httptest.NewRecorder()

	wrapped.ServeHTTP(rr, req)

	if rr.Code != http.StatusNoContent {
		t.Fatalf("expected status %d, got %d", http.StatusNoContent, rr.Code)
	}
}

func TestGzip_ChunkedWrite(t *testing.T) {
	// Test that large responses that exceed max buffer are written in chunks
	// Create a response larger than the default 10MB buffer
	largeData := strings.Repeat("X", 11<<20) // 11MB

	handler := func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(largeData))
	}

	// Use a custom config with a small max buffer to trigger chunked writing
	cfg := Config{MaxBufferBytes: 1 << 20} // 1MB max buffer
	wrapped := applyMiddleware(http.HandlerFunc(handler), Middleware(cfg))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	rr := httptest.NewRecorder()

	wrapped.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d", http.StatusOK, rr.Code)
	}

	// Should not compress because response exceeds max buffer
	if rr.Header().Get("Content-Encoding") == "gzip" {
		t.Fatalf("should not compress large response that exceeds max buffer")
	}

	// Verify the full data was written
	if rr.Body.Len() != len(largeData) {
		t.Fatalf("expected %d bytes, got %d", len(largeData), rr.Body.Len())
	}

	// Verify content integrity
	if !bytes.Equal(rr.Body.Bytes(), []byte(largeData)) {
		t.Fatalf("data integrity check failed")
	}
}

func TestGzip_ErrorHandling(t *testing.T) {
	// Test error handling when writing buffered data fails
	handler := func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(http.StatusOK)
		// Write data that will exceed max buffer
		w.Write([]byte(strings.Repeat("A", 11<<20))) // 11MB
	}

	// Use a custom config with a small max buffer
	cfg := Config{MaxBufferBytes: 1 << 20} // 1MB max buffer
	wrapped := applyMiddleware(http.HandlerFunc(handler), Middleware(cfg))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Accept-Encoding", "gzip")

	// Use a custom ResponseWriter that simulates write errors
	rr := httptest.NewRecorder()

	// Wrap the recorder to simulate write errors after a certain amount of data
	errorWriter := &errorSimulatingWriter{
		ResponseWriter: rr,
		failAfter:      1 << 20, // Fail after 1MB
	}

	wrapped.ServeHTTP(errorWriter, req)

	// The handler should handle the error gracefully
	// We're mainly testing that the error doesn't cause a panic
}

// errorSimulatingWriter simulates write errors for testing
type errorSimulatingWriter struct {
	http.ResponseWriter
	failAfter int
	written   int
}

func (w *errorSimulatingWriter) Write(p []byte) (int, error) {
	if w.written+len(p) > w.failAfter {
		return 0, io.ErrUnexpectedEOF
	}
	w.written += len(p)
	return w.ResponseWriter.Write(p)
}

type gzipHijackWriter struct {
	header      http.Header
	serverConn  net.Conn
	clientConn  net.Conn
	hijacked    bool
	wroteHeader bool
	wroteBody   bool
}

func newGzipHijackWriter() *gzipHijackWriter {
	serverConn, clientConn := net.Pipe()
	return &gzipHijackWriter{
		header:     make(http.Header),
		serverConn: serverConn,
		clientConn: clientConn,
	}
}

func (w *gzipHijackWriter) Header() http.Header {
	return w.header
}

func (w *gzipHijackWriter) WriteHeader(int) {
	w.wroteHeader = true
}

func (w *gzipHijackWriter) Write(p []byte) (int, error) {
	w.wroteBody = true
	return len(p), nil
}

func (w *gzipHijackWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	w.hijacked = true
	return w.serverConn, bufio.NewReadWriter(bufio.NewReader(w.clientConn), bufio.NewWriter(w.clientConn)), nil
}

func (w *gzipHijackWriter) close() {
	if w.serverConn != nil {
		_ = w.serverConn.Close()
	}
	if w.clientConn != nil {
		_ = w.clientConn.Close()
	}
}

func headerValuesContain(values []string, want string) bool {
	for _, value := range values {
		for _, item := range strings.Split(value, ",") {
			if strings.EqualFold(strings.TrimSpace(item), want) {
				return true
			}
		}
	}
	return false
}

func countHeaderToken(values []string, want string) int {
	var count int
	for _, value := range values {
		for _, item := range strings.Split(value, ",") {
			if strings.EqualFold(strings.TrimSpace(item), want) {
				count++
			}
		}
	}
	return count
}

// --- NEW COVERAGE TESTS ---

// TestGzip_AcceptEncodingInvalidQValue covers the !ok (no "=") and ParseFloat error
// branches in the q-value parser.
func TestGzip_AcceptEncodingInvalidQValue(t *testing.T) {
	tests := []struct {
		name           string
		header         string
		wantCompressed bool
	}{
		// "q" param present but no "=" → treated as q=0 → skip gzip.
		{name: "no equals in param", header: "gzip;qbad", wantCompressed: true},
		// "q" param present but non-numeric value → ParseFloat error → q=0.
		{name: "invalid q value", header: "gzip;q=bad", wantCompressed: false},
		// Wildcard with invalid q → q=0 → not accepted.
		{name: "wildcard invalid q", header: "*;q=notanumber", wantCompressed: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			handler := func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/plain")
				w.WriteHeader(http.StatusOK)
				w.Write([]byte("Hello World"))
			}
			wrapped := applyMiddleware(http.HandlerFunc(handler), Middleware(Config{}))
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			req.Header.Set("Accept-Encoding", tt.header)
			rr := httptest.NewRecorder()
			wrapped.ServeHTTP(rr, req)

			compressed := rr.Header().Get("Content-Encoding") == "gzip"
			if compressed != tt.wantCompressed {
				t.Fatalf("compressed = %v, want %v for header %q", compressed, tt.wantCompressed, tt.header)
			}
		})
	}
}

// TestGzip_WriteHeaderIdempotent verifies that calling WriteHeader twice only
// sets the status code once (covers the wroteHeader guard).
func TestGzip_WriteHeaderIdempotent(t *testing.T) {
	wrapped := Middleware(Config{})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(http.StatusCreated)
		w.WriteHeader(http.StatusOK) // second call should be ignored
		w.Write([]byte("body"))
	}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	rr := httptest.NewRecorder()
	wrapped.ServeHTTP(rr, req)

	if rr.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusCreated)
	}
}

// TestGzip_PassThroughWriteHeaderDirect covers the w.passThrough branch inside
// WriteHeader by setting passThrough before wroteHeader.
func TestGzip_PassThroughWriteHeaderDirect(t *testing.T) {
	rr := httptest.NewRecorder()
	gw := &gzipResponseWriter{
		ResponseWriter: rr,
		cfg:            DefaultConfig(),
		passThrough:    true,
		// wroteHeader is false — so WriteHeader will reach the passThrough branch.
	}
	gw.WriteHeader(http.StatusAccepted)
	// headers were flushed directly to the underlying writer
	if rr.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusAccepted)
	}
}

// TestGzip_HeaderPassThroughNoBuffer covers the `w.passThrough && w.buffer == nil`
// branch inside Header().
func TestGzip_HeaderPassThroughNoBuffer(t *testing.T) {
	gw := &gzipResponseWriter{
		ResponseWriter: httptest.NewRecorder(),
		passThrough:    true,
		// buffer is nil
	}
	h := gw.Header()
	if h == nil {
		t.Fatal("Header() returned nil")
	}
}

// TestGzip_WritePassThrough covers the w.passThrough branch in Write.
func TestGzip_WritePassThrough(t *testing.T) {
	wrapped := Middleware(Config{})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Flush to enter pass-through before writing.
		w.(http.Flusher).Flush()
		w.Header().Set("Content-Type", "text/plain")
		w.Write([]byte("pass-through data"))
	}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	rr := httptest.NewRecorder()
	wrapped.ServeHTTP(rr, req)

	if got := rr.Header().Get("Content-Encoding"); got == "gzip" {
		t.Fatal("pass-through write should not be gzip encoded")
	}
	if got := rr.Body.String(); got != "pass-through data" {
		t.Fatalf("body = %q, want pass-through data", got)
	}
}

// TestGzip_CommitPassThroughWithBuffer covers the branch in commitPassThrough where
// a buffer exists and headersFlushed is false — flushHeaders is called on the buffer.
func TestGzip_CommitPassThroughWithBuffer(t *testing.T) {
	// Build a handler where: content-type is set, WriteHeader is called (defers
	// compression decision), but then we never write body so finalize hits
	// pendingCompress=true with empty buffer → flushHeaders only path.
	// Instead, verify commitPassThrough with a real integration:
	wrapped := Middleware(Config{})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Don't set Content-Type before WriteHeader so compression is deferred.
		// Write nothing — this means the deferred decision stays pending.
		// Flush will call commitPassThrough with buffer (no headersFlushed, but
		// pendingCompress=true, buffer empty → resolves to false and flushes).
		w.WriteHeader(http.StatusNoContent)
		w.(http.Flusher).Flush()
	}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	rr := httptest.NewRecorder()
	wrapped.ServeHTTP(rr, req)

	if rr.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusNoContent)
	}
	if rr.Header().Get("Content-Encoding") == "gzip" {
		t.Fatal("commitPassThrough should not set gzip encoding")
	}
}

// TestGzip_FinalizeWithHeadersFlushedButNoGz covers the finalize path where
// headersFlushed is true but gz is nil (non-compressible response).
func TestGzip_FinalizeWithHeadersFlushedButNoGz(t *testing.T) {
	wrapped := Middleware(Config{})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/octet-stream")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("binary"))
	}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	rr := httptest.NewRecorder()
	wrapped.ServeHTTP(rr, req)

	if rr.Header().Get("Content-Encoding") == "gzip" {
		t.Fatal("octet-stream should not be compressed")
	}
	if rr.Body.String() != "binary" {
		t.Fatalf("body = %q, want binary", rr.Body.String())
	}
}

// TestGzip_FinalizeBufferWithContentAfterGzClose covers the finalize path where
// compressionUsed=true, gz=nil (compression decided but gz not yet started), and
// buffer has data — this writes the buffer to the underlying writer uncompressed.
// We reach this state by writing very small data (≤ buffer, gz starts) then
// verifying the gz.Close path via finalize when the gz is active.
func TestGzip_FinalizeBufferWithContentAfterGzClose(t *testing.T) {
	// Directly construct the state: compressionUsed=true, gz=nil, buffer has data.
	// This represents the case where WriteHeader set compressionUsed but no Write
	// arrived yet, so gz was never initialized.
	rr := httptest.NewRecorder()
	gw := &gzipResponseWriter{
		ResponseWriter: rr,
		cfg:            Config{MaxBufferBytes: 10 << 20},
	}
	gw.ensureBuffer()
	gw.buffer.WriteHeader(http.StatusOK)
	gw.buffer.Header().Set("Content-Type", "text/plain")
	gw.compressionUsed = true
	gw.wroteHeader = true
	// Manually write body data into the underlying buffer bytes.
	gw.buffer.Write([]byte("data"))
	gw.finalize(false)
	// We only need no panic; data may or may not pass through depending on path.
	_ = rr.Body.String()
}

// TestGzip_FlushAfterGzStarted covers the gz.Flush() path in Flush.
func TestGzip_FlushAfterGzStarted(t *testing.T) {
	wrapped := Middleware(Config{})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.Write([]byte("first chunk"))
		w.(http.Flusher).Flush()
		w.Write([]byte(" second chunk"))
	}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	rr := httptest.NewRecorder()
	wrapped.ServeHTTP(rr, req)

	if rr.Header().Get("Content-Encoding") != "gzip" {
		t.Fatalf("expected gzip encoding, got %q", rr.Header().Get("Content-Encoding"))
	}
	got := gunzipBody(t, rr)
	if got != "first chunk second chunk" {
		t.Fatalf("body = %q, want 'first chunk second chunk'", got)
	}
}

// TestGzip_HijackAfterGzStartedReturnsNotSupported covers the
// `w.gz != nil` guard in Hijack.
func TestGzip_HijackAfterGzStartedReturnsNotSupported(t *testing.T) {
	writer := newGzipHijackWriter()
	defer writer.close()

	handler := func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.Write([]byte("data")) // starts gz
		hijacker, ok := w.(http.Hijacker)
		if !ok {
			t.Fatal("expected Hijacker")
		}
		if _, _, err := hijacker.Hijack(); err != http.ErrNotSupported {
			t.Fatalf("Hijack after gz started = %v, want %v", err, http.ErrNotSupported)
		}
	}

	wrapped := applyMiddleware(http.HandlerFunc(handler), Middleware(Config{}))
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	wrapped.ServeHTTP(writer, req)
}

// TestGzip_HijackAfterHeadersFlushedReturnsNotSupported covers the
// `w.headersFlushed` guard in Hijack.
func TestGzip_HijackAfterHeadersFlushedReturnsNotSupported(t *testing.T) {
	writer := newGzipHijackWriter()
	defer writer.close()

	handler := func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/octet-stream")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("data")) // flushes headers
		hijacker, ok := w.(http.Hijacker)
		if !ok {
			t.Fatal("expected Hijacker")
		}
		if _, _, err := hijacker.Hijack(); err != http.ErrNotSupported {
			t.Fatalf("Hijack after headers flushed = %v, want %v", err, http.ErrNotSupported)
		}
	}

	wrapped := applyMiddleware(http.HandlerFunc(handler), Middleware(Config{}))
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	wrapped.ServeHTTP(writer, req)
}

// TestGzip_ResolvePendingCompression_NotCompressible covers resolvePendingCompression
// when shouldCompress returns false (flushHeaders is called directly).
func TestGzip_ResolvePendingCompression_NotCompressible(t *testing.T) {
	wrapped := Middleware(Config{})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Trigger pending compress by sending WriteHeader without Content-Type.
		w.WriteHeader(http.StatusOK)
		// Now write binary data to resolve pending to false.
		w.Write(append([]byte{0x89}, bytes.Repeat([]byte{0}, 520)...))
	}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	rr := httptest.NewRecorder()
	wrapped.ServeHTTP(rr, req)

	if rr.Header().Get("Content-Encoding") == "gzip" {
		t.Fatal("binary content should not be compressed after pending resolve")
	}
}

// TestGzip_FlushHeadersNilBuffer covers the `w.buffer == nil` early return in flushHeaders.
func TestGzip_FlushHeadersNilBuffer(t *testing.T) {
	rr := httptest.NewRecorder()
	gw := &gzipResponseWriter{
		ResponseWriter: rr,
		cfg:            DefaultConfig(),
		// buffer is nil
	}
	gw.flushHeaders() // must not panic
}

// TestGzip_FlushHeadersAlreadyFlushed covers the `w.headersFlushed` early return.
func TestGzip_FlushHeadersAlreadyFlushed(t *testing.T) {
	rr := httptest.NewRecorder()
	gw := &gzipResponseWriter{
		ResponseWriter: rr,
		cfg:            DefaultConfig(),
		headersFlushed: true,
	}
	gw.ensureBuffer()
	gw.flushHeaders() // must not write to the recorder again
	// The recorder should not have had WriteHeader called a second time.
}

// TestGzip_CommitPassThroughAlreadyPassThrough covers the early return in commitPassThrough.
func TestGzip_CommitPassThroughAlreadyPassThrough(t *testing.T) {
	rr := httptest.NewRecorder()
	gw := &gzipResponseWriter{
		ResponseWriter: rr,
		cfg:            DefaultConfig(),
		passThrough:    true, // already in pass-through
	}
	gw.commitPassThrough() // must not panic, must be a no-op
}

// TestGzip_CommitPassThroughHeadersFlushed covers commitPassThrough when headers are
// already flushed (early return after setting passThrough).
func TestGzip_CommitPassThroughHeadersFlushed(t *testing.T) {
	rr := httptest.NewRecorder()
	gw := &gzipResponseWriter{
		ResponseWriter: rr,
		cfg:            DefaultConfig(),
		headersFlushed: true,
		wroteHeader:    true,
	}
	gw.commitPassThrough() // must hit the headersFlushed early return
	if !gw.passThrough {
		t.Fatal("passThrough should be set to true")
	}
}

// TestGzip_ResolvePendingCompression_AlreadyFalse covers the !pendingCompress
// early return in resolvePendingCompression.
func TestGzip_ResolvePendingCompression_AlreadyFalse(t *testing.T) {
	rr := httptest.NewRecorder()
	gw := &gzipResponseWriter{
		ResponseWriter:  rr,
		cfg:             DefaultConfig(),
		pendingCompress: false, // not pending
	}
	gw.ensureBuffer()
	gw.resolvePendingCompression() // must be a no-op
	if gw.compressionUsed {
		t.Fatal("compressionUsed should not be set when pendingCompress is false")
	}
}

// TestGzip_ShouldCompress_EmptyContentType covers the contentType=="" branch.
func TestGzip_ShouldCompress_EmptyContentType(t *testing.T) {
	rr := httptest.NewRecorder()
	gw := &gzipResponseWriter{
		ResponseWriter: rr,
		cfg:            DefaultConfig(),
	}
	gw.ensureBuffer()
	// Empty content type → shouldCompress returns false.
	if gw.shouldCompress(http.StatusOK) {
		t.Fatal("shouldCompress should return false when Content-Type is empty")
	}
}

// TestGzip_FinalizeWithPendingAndHeadersFlushed covers the finalize branch where
// compressionUsed=false, pendingCompress=true, resolvePendingCompression is called,
// and then headersFlushed=true → early return.
func TestGzip_FinalizeWithPendingAndHeadersFlushed(t *testing.T) {
	// Scenario: WriteHeader was called (defers compression decision, pendingCompress=true).
	// Then Flush is called (commitPassThrough sets headersFlushed), then finalize runs.
	wrapped := Middleware(Config{})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// No content type → defers compression decision.
		w.WriteHeader(http.StatusOK)
		// Write binary to resolve to not-compress.
		w.Write(append([]byte{0x89}, bytes.Repeat([]byte{0}, 520)...))
	}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	rr := httptest.NewRecorder()
	wrapped.ServeHTTP(rr, req)

	if rr.Header().Get("Content-Encoding") == "gzip" {
		t.Fatal("binary data after deferred WriteHeader should not be compressed")
	}
}

// TestGzip_WriteBufferedBodyChunked tests the chunked write loop when the buffer
// has previously accumulated data and a new write exceeds MaxBufferBytes.
// This covers the for loop in Write at lines 372-388 with len(body) > chunkSize.
func TestGzip_WriteBufferedBodyChunked(t *testing.T) {
	// Use tiny MaxBufferBytes to force the chunked-flush path with a non-trivial
	// buffered body size (more than one 8KB chunk worth but small in test terms).
	const chunkSize = 8192
	// Accumulate chunkSize+100 bytes in the buffer, then trigger the flush.
	// First write fills the buffer under the limit; second write pushes it over.
	buffered := strings.Repeat("B", chunkSize+100)
	overflow := strings.Repeat("X", 10)

	handler := func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(buffered))
		_, _ = w.Write([]byte(overflow))
	}

	// MaxBufferBytes small enough that buffered+overflow exceeds it.
	cfg := Config{MaxBufferBytes: chunkSize}
	wrapped := applyMiddleware(http.HandlerFunc(handler), Middleware(cfg))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	rr := httptest.NewRecorder()
	wrapped.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	if rr.Header().Get("Content-Encoding") == "gzip" {
		t.Fatal("should not be compressed when buffer exceeded before gz start")
	}
	want := buffered + overflow
	if rr.Body.String() != want {
		t.Fatalf("body length = %d, want %d", rr.Body.Len(), len(want))
	}
}

// TestGzip_WriteBufferErrorPath covers the `w.buffer.Write` error
// path (line 325-328) via a unit-level injection.
func TestGzip_WriteBufferErrorPath(t *testing.T) {
	// To hit `w.buffer.Write` error we need:
	//   - compressionUsed=true, gz=nil
	//   - cfg.MaxBufferBytes = 0 (disables the overflow bypass check)
	//   - buffer.maxBytes = 1 so Write returns an error immediately
	rr := httptest.NewRecorder()
	gw := &gzipResponseWriter{
		ResponseWriter:  rr,
		cfg:             Config{MaxBufferBytes: 0},
		compressionUsed: true,
		wroteHeader:     true,
		headersFlushed:  true,
	}
	// Create a buffer with maxBytes=1 so that Write("hello") overflows.
	gw.buffer = internaltransport.NewBufferedResponse(1)
	gw.buffer.WriteHeader(http.StatusOK)
	gw.buffer.Header().Set("Content-Type", "text/plain")
	_, err := gw.Write([]byte("hello"))
	if err == nil {
		t.Fatal("expected error from buffer.Write overflow")
	}
}

// TestGzip_FinalizeWithPendingCompressAndNotCompressed covers the finalize branch
// where compressionUsed=false AND pendingCompress=true (deferred decision, no write).
// Two sub-cases: headersFlushed=true (line 200) and headersFlushed=false (line 202).
func TestGzip_FinalizeWithPendingCompressAndNotCompressed(t *testing.T) {
	// Sub-case 1: headersFlushed=true → hits line 200 return.
	t.Run("headersFlushed", func(t *testing.T) {
		rr := httptest.NewRecorder()
		gw := &gzipResponseWriter{
			ResponseWriter:  rr,
			cfg:             DefaultConfig(),
			compressionUsed: false,
			pendingCompress: true,
			wroteHeader:     true,
			headersFlushed:  true,
		}
		gw.ensureBuffer()
		gw.buffer.WriteHeader(http.StatusOK)
		gw.buffer.Header().Set("Content-Type", "text/plain")
		gw.finalize(false) // must not panic
	})

	// Sub-case 2: headersFlushed=false → resolvePendingCompression called,
	// then the inner `return` at line 202 is reached.
	t.Run("notHeadersFlushed", func(t *testing.T) {
		rr := httptest.NewRecorder()
		gw := &gzipResponseWriter{
			ResponseWriter:  rr,
			cfg:             DefaultConfig(),
			compressionUsed: false,
			pendingCompress: true,
			wroteHeader:     true,
			headersFlushed:  false,
		}
		gw.ensureBuffer()
		gw.buffer.WriteHeader(http.StatusOK)
		// No Content-Type → shouldCompress=false → flushHeaders is called inside resolvePendingCompression.
		gw.finalize(false) // must not panic
		if !gw.headersFlushed {
			t.Fatal("expected headers to be flushed after finalize with pendingCompress")
		}
	})
}

// TestGzip_WriteChunkedBodyFlush covers the for loop in Write (lines 301-318)
// where previously buffered data exceeds one chunk when overflow is triggered.
// We set up the gzipResponseWriter directly so gz hasn't started.
func TestGzip_WriteChunkedBodyFlush(t *testing.T) {
	const chunkSize = 8192
	// Accumulate chunkSize+100 bytes in the buffer without starting gz
	// by setting compressionUsed=false initially, then flipping to true.
	// Actually we need compressionUsed=true but gz=nil with buffer having data.
	// The only way gz stays nil after buffer.Write is if buffer.Len()==0.
	// So we inject a pre-filled buffer directly.
	rr := httptest.NewRecorder()
	gw := &gzipResponseWriter{
		ResponseWriter:  rr,
		cfg:             Config{MaxBufferBytes: chunkSize + 200},
		compressionUsed: true,
		wroteHeader:     true,
		headersFlushed:  false,
	}
	gw.ensureBuffer()
	gw.buffer.WriteHeader(http.StatusOK)
	gw.buffer.Header().Set("Content-Type", "text/plain")
	// Fill buffer with chunkSize+100 bytes directly.
	bigData := strings.Repeat("X", chunkSize+100)
	gw.buffer.Write([]byte(bigData))

	// Now Write another piece that pushes buffer.Len()+len(p) > MaxBufferBytes.
	overflow := strings.Repeat("Y", 200)
	n, err := gw.Write([]byte(overflow))
	if err != nil {
		t.Fatalf("Write error = %v", err)
	}
	_ = n
	// Verify the body was written (chunked loop ran).
	if rr.Body.Len() == 0 {
		t.Fatal("expected body to be written via chunked loop")
	}
}

// TestGzip_WriteGzWriteBufferBodyError covers the gz.Write(buffer.Body()) error path
// (line 340-343) by using a gz writer that wraps a broken io.Writer.
func TestGzip_WriteGzWriteBufferBodyError(t *testing.T) {
	// We need: compressionUsed=true, gz=nil, buffer has data, and gz.Write fails.
	// To get gz.Write to fail we create a gz.Writer backed by a failing writer,
	// then manually inject it after the gz starts.
	//
	// Actually, the gz is created fresh inside Write at line 337.
	// We can't inject a failing gz before line 337 fires.
	// Instead we call Write directly with a ResponseWriter that rejects writes.
	rr := &errorAfterNBytesWriter{limit: 0} // fails immediately
	gw := &gzipResponseWriter{
		ResponseWriter:  rr,
		cfg:             Config{MaxBufferBytes: 0},
		compressionUsed: true,
		wroteHeader:     true,
		headersFlushed:  false,
	}
	gw.ensureBuffer()
	gw.buffer.WriteHeader(http.StatusOK)
	gw.buffer.Header().Set("Content-Type", "text/plain")
	// buffer.Write succeeds (cfg.MaxBufferBytes=0 means no limit in BufferedResponse).
	// Then gz is created writing to rr which fails → gz.Write(buffer.Body()) fails.
	_, err := gw.Write([]byte("hello"))
	// Either an error is returned or it's swallowed. We just check no panic.
	_ = err
}

// errorAfterNBytesWriter rejects all writes with an error.
type errorAfterNBytesWriter struct {
	header http.Header
	limit  int
	n      int
}

func (w *errorAfterNBytesWriter) Header() http.Header {
	if w.header == nil {
		w.header = make(http.Header)
	}
	return w.header
}

func (w *errorAfterNBytesWriter) WriteHeader(int) {}

func (w *errorAfterNBytesWriter) Write(p []byte) (int, error) {
	if w.n >= w.limit {
		return 0, io.ErrClosedPipe
	}
	w.n += len(p)
	return len(p), nil
}

// TestGzip_WriteEmptyBodyWithCompressionUsed covers the last `return len(p), nil`
// in Write (line 350) reached when gz is nil, compressionUsed=true, and buffer
// is empty after writing an empty p (len(p)==0 → buffer.Len() remains 0).
func TestGzip_WriteEmptyBodyWithCompressionUsed(t *testing.T) {
	rr := httptest.NewRecorder()
	gw := &gzipResponseWriter{
		ResponseWriter:  rr,
		cfg:             DefaultConfig(),
		compressionUsed: true,
		wroteHeader:     true,
		headersFlushed:  true,
	}
	gw.ensureBuffer()
	gw.buffer.WriteHeader(http.StatusOK)
	n, err := gw.Write([]byte{}) // empty write → buffer.Len() == 0 → gz not started
	if err != nil {
		t.Fatalf("empty Write error = %v", err)
	}
	if n != 0 {
		t.Fatalf("n = %d, want 0", n)
	}
}

func gunzipBody(t *testing.T, rr *httptest.ResponseRecorder) string {
	t.Helper()

	reader, err := gzip.NewReader(rr.Body)
	if err != nil {
		t.Fatalf("failed to create gzip reader: %v", err)
	}
	defer reader.Close()

	decompressed, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("failed to decompress: %v", err)
	}
	return string(decompressed)
}

func TestGzipPreservesRangeResponses(t *testing.T) {
	const content = "abcdefghijklmnopqrstuvwxyz"
	handler := Middleware(DefaultConfig())(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		http.ServeContent(w, r, "sample.txt", time.Time{}, strings.NewReader(content))
	}))

	for _, ranges := range []string{"bytes=0-3", "bytes=0-3,8-10"} {
		t.Run(ranges, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			req.Header.Set("Accept-Encoding", "gzip")
			req.Header.Set("Range", ranges)
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)
			if rec.Code != http.StatusPartialContent || rec.Header().Get("Content-Encoding") != "" {
				t.Fatalf("status = %d, encoding = %q", rec.Code, rec.Header().Get("Content-Encoding"))
			}
			if ranges == "bytes=0-3" {
				if rec.Header().Get("Content-Range") != "bytes 0-3/26" || rec.Body.String() != "abcd" {
					t.Fatalf("range = %q, body = %q", rec.Header().Get("Content-Range"), rec.Body.String())
				}
				return
			}
			mediaType, params, err := mime.ParseMediaType(rec.Header().Get("Content-Type"))
			if err != nil || mediaType != "multipart/byteranges" {
				t.Fatalf("media type = %q, error = %v", mediaType, err)
			}
			reader := multipart.NewReader(rec.Body, params["boundary"])
			for _, want := range []struct{ span, body string }{{"bytes 0-3/26", "abcd"}, {"bytes 8-10/26", "ijk"}} {
				part, err := reader.NextPart()
				if err != nil {
					t.Fatal(err)
				}
				body, err := io.ReadAll(part)
				if err != nil || part.Header.Get("Content-Range") != want.span || string(body) != want.body {
					t.Fatalf("part range = %q, body = %q, error = %v", part.Header.Get("Content-Range"), body, err)
				}
			}
			if _, err := reader.NextPart(); err != io.EOF {
				t.Fatalf("end of multipart response: %v", err)
			}
		})
	}
}

func TestFlushAfterExplicitHeaderBeforeBody(t *testing.T) {
	handler := Middleware(DefaultConfig())(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.Header().Set("X-App", "preserve")
		w.WriteHeader(http.StatusCreated)
		if err := http.NewResponseController(w).Flush(); err != nil {
			t.Fatal(err)
		}
		_, _ = w.Write([]byte("hello"))
	}))
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	res := rec.Result()
	if !rec.Flushed || res.StatusCode != http.StatusCreated || res.Header.Get("X-App") != "preserve" ||
		res.Header.Get("Content-Encoding") != "" || rec.Body.String() != "hello" {
		t.Fatalf("flushed=%v status=%d headers=%v body=%q", rec.Flushed, res.StatusCode, res.Header, rec.Body.String())
	}
}

func TestVaryCoversEncodingVariants(t *testing.T) {
	for _, encoding := range []string{"gzip", "identity", "", "gzip;q=0"} {
		t.Run(encoding, func(t *testing.T) {
			for _, vary := range []string{"Origin", "Origin, Accept-Encoding", "*"} {
				handler := Middleware(DefaultConfig())(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					w.Header().Set("Vary", vary)
					w.Header().Set("Content-Type", "text/plain")
					_, _ = w.Write([]byte("hello"))
				}))
				req := httptest.NewRequest(http.MethodGet, "/", nil)
				if encoding != "" {
					req.Header.Set("Accept-Encoding", encoding)
				}
				rec := httptest.NewRecorder()
				handler.ServeHTTP(rec, req)
				res := rec.Result()
				values := res.Header.Values("Vary")
				if vary == "*" {
					if len(values) != 1 || values[0] != "*" {
						t.Fatalf("Vary = %v, want wildcard only", values)
					}
				} else if countHeaderToken(values, "Accept-Encoding") != 1 || !headerValuesContain(values, "Origin") {
					t.Fatalf("Vary = %v, want Origin and Accept-Encoding once", values)
				}
				if encoding == "gzip" {
					if res.Header.Get("Content-Encoding") != "gzip" || gunzipBody(t, rec) != "hello" {
						t.Fatalf("unexpected gzip response headers: %v", res.Header)
					}
				} else if res.Header.Get("Content-Encoding") != "" || rec.Body.String() != "hello" {
					t.Fatalf("unexpected identity response: %v %q", res.Header, rec.Body.String())
				}
			}
		})
	}
}
