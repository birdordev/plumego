package debug

import (
	"bufio"
	"bytes"
	"net"
	"net/http"
	"strings"

	"github.com/birdordev/plumego/contract"
	internaltransport "github.com/birdordev/plumego/internal/httputil"
	"github.com/birdordev/plumego/middleware"
)

const defaultMaxBodyBytes = 64 << 10

// Config controls how debug error responses are formatted.
//
// This middleware is useful during development to provide detailed error information.
// It replaces empty or plain text error responses with structured JSON error messages.
//
// Example:
//
//	import "github.com/birdordev/plumego/middleware/debug"
//
//	config := debug.Config{
//		IncludeRequest: true,  // Include request method and path
//		IncludeQuery:   true,  // Include query parameters
//		IncludeBody:    false, // Don't include response body (security)
//		NotFoundHint:   "Try /api/v1/users", // Hint for 404 errors
//	}
//	handler := debug.Middleware(config)(myHandler)
//
// Security note: This middleware should only be used in development environments.
// In production, consider using a proper error logging and monitoring system.
type Config struct {
	// IncludeRequest controls whether to include request method and path in error details
	IncludeRequest bool

	// IncludeQuery controls whether to include query parameters in error details
	IncludeQuery bool

	// IncludeBody controls whether to include response body in error details
	// Note: This may expose sensitive information, use with caution
	IncludeBody bool

	// MaxBodyBytes is the maximum response body captured for debug replacement.
	// If the response exceeds the limit, the original response is passed through
	// and debug replacement is skipped. A non-positive value uses the default.
	MaxBodyBytes int

	// NotFoundHint provides a hint message for 404 errors
	NotFoundHint string
}

// DefaultConfig returns a safe default for debug errors.
func DefaultConfig() Config {
	return Config{
		IncludeRequest: true,
		IncludeQuery:   true,
		MaxBodyBytes:   defaultMaxBodyBytes,
	}
}

// DebugErrors replaces empty/plain error responses with structured JSON.
//
// This middleware intercepts error responses and replaces them with structured JSON
// error messages containing additional debugging information.
//
// Example:
//
//	import "github.com/birdordev/plumego/middleware/debug"
//
//	// Use default configuration
//	handler := debug.Middleware(debug.DefaultConfig())(myHandler)
//
//	// Or with custom configuration
//	config := debug.Config{
//		IncludeRequest: true,
//		IncludeQuery:   true,
//		NotFoundHint:   "Try /api/v1/users",
//	}
//	handler := debug.Middleware(config)(myHandler)
//
// The middleware skips debugging for:
//   - WebSocket connections
//   - Server-Sent Events (SSE)
//   - CONNECT requests
//   - Responses with non-plain-text content types
//
// Error response format:
//
//	{
//	  "status": 404,
//	  "code": "not_found",
//	  "message": "Not Found",
//	  "category": "client",
//	  "details": {
//	    "method": "GET",
//	    "path": "/api/missing",
//	    "query": "param=value",
//	    "hint": "Try /api/v1/users"
//	  }
//	}
func Middleware(config Config) middleware.Middleware {
	cfg := config
	if cfg.MaxBodyBytes <= 0 {
		cfg.MaxBodyBytes = defaultMaxBodyBytes
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if shouldSkipDebugErrors(r) {
				next.ServeHTTP(w, r)
				return
			}

			rec := newDebugErrorRecorder(w, cfg.MaxBodyBytes)
			next.ServeHTTP(rec, r)

			if rec.passthrough {
				return
			}

			status := rec.statusCode()
			body := rec.body.Bytes()

			if status < http.StatusBadRequest || !shouldReplaceError(rec.header, body) {
				rec.flushTo()
				return
			}

			internaltransport.CopyHeaders(w.Header(), rec.header)
			w.Header().Del("Content-Length")
			_ = contract.WriteError(w, r, debugErrorPayload(status, r, cfg, body))
		})
	}
}

type debugErrorRecorder struct {
	dst         http.ResponseWriter
	header      http.Header
	status      int
	body        bytes.Buffer
	maxBytes    int
	passthrough bool
}

func newDebugErrorRecorder(dst http.ResponseWriter, maxBytes int) *debugErrorRecorder {
	return &debugErrorRecorder{
		dst:      dst,
		header:   make(http.Header),
		maxBytes: maxBytes,
	}
}

func (r *debugErrorRecorder) Header() http.Header {
	return r.header
}

func (r *debugErrorRecorder) Unwrap() http.ResponseWriter {
	return r.dst
}

func (r *debugErrorRecorder) WriteHeader(status int) {
	if r.status == 0 {
		r.status = status
		if r.passthrough {
			r.flushHeaders()
		}
	}
}

func (r *debugErrorRecorder) Write(p []byte) (int, error) {
	if r.status == 0 {
		r.status = http.StatusOK
	}
	if r.passthrough {
		return internaltransport.SafeWrite(r.dst, p)
	}
	if r.maxBytes > 0 && r.body.Len()+len(p) > r.maxBytes {
		r.passthrough = true
		r.flushHeaders()
		if r.body.Len() > 0 {
			if _, err := internaltransport.SafeWrite(r.dst, r.body.Bytes()); err != nil {
				r.body.Reset()
				return 0, err
			}
			r.body.Reset()
		}
		n, err := internaltransport.SafeWrite(r.dst, p)
		return n, err
	}
	return r.body.Write(p)
}

func (r *debugErrorRecorder) Flush() {
	r.commitPassthrough()
	internaltransport.FlushIfSupported(r.dst)
}

func (r *debugErrorRecorder) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	if r.body.Len() > 0 || r.status != 0 {
		r.commitPassthrough()
	} else {
		r.passthrough = true
	}
	return internaltransport.HijackIfSupported(r.dst)
}

func (r *debugErrorRecorder) statusCode() int {
	if r.status == 0 {
		return http.StatusOK
	}
	return r.status
}

func (r *debugErrorRecorder) flushTo() {
	internaltransport.CommitHeadersCopy(r.dst, r.header, r.statusCode())
	_, _ = internaltransport.SafeWrite(r.dst, r.body.Bytes())
}

func (r *debugErrorRecorder) flushHeaders() {
	if r.dst == nil {
		return
	}
	internaltransport.CommitHeadersCopy(r.dst, r.header, r.statusCode())
}

func (r *debugErrorRecorder) commitPassthrough() {
	if r.passthrough {
		return
	}
	r.passthrough = true
	r.flushHeaders()
	if r.body.Len() > 0 {
		_, _ = internaltransport.SafeWrite(r.dst, r.body.Bytes())
		r.body.Reset()
	}
}

func shouldSkipDebugErrors(r *http.Request) bool {
	if r == nil {
		return false
	}

	if strings.EqualFold(r.Header.Get("Upgrade"), "websocket") {
		return true
	}

	accept := strings.ToLower(r.Header.Get("Accept"))
	if strings.Contains(accept, "text/event-stream") {
		return true
	}

	if r.Method == http.MethodConnect {
		return true
	}

	return false
}

func shouldReplaceError(header http.Header, body []byte) bool {
	contentType := strings.ToLower(strings.TrimSpace(header.Get("Content-Type")))
	if isStreamingContentType(contentType) {
		return false
	}
	if len(body) == 0 {
		return true
	}
	return strings.HasPrefix(contentType, "text/plain")
}

func isStreamingContentType(contentType string) bool {
	return strings.Contains(contentType, "stream")
}

func debugErrorPayload(status int, r *http.Request, cfg Config, body []byte) contract.APIError {
	message := strings.ToLower(http.StatusText(status))
	code := strings.ReplaceAll(message, " ", "_")

	details := map[string]any{}
	if cfg.IncludeRequest {
		details["method"] = r.Method
		details["path"] = r.URL.Path
	}
	if cfg.IncludeQuery && r.URL.RawQuery != "" {
		details["query"] = r.URL.RawQuery
	}
	if status == http.StatusNotFound && cfg.NotFoundHint != "" {
		details["hint"] = cfg.NotFoundHint
	}
	if cfg.IncludeBody && len(body) > 0 {
		details["response_preview"] = truncateBody(body, 1024)
	}
	if len(details) == 0 {
		details = nil
	}

	return contract.NewErrorBuilder().
		Type(debugErrorType(status)).
		Code(code).
		Message(message).
		Details(details).
		Build()
}

func debugErrorType(status int) contract.ErrorType {
	switch status {
	case http.StatusNotFound:
		return contract.TypeNotFound
	case http.StatusMethodNotAllowed:
		return contract.TypeMethodNotAllowed
	case http.StatusNotAcceptable:
		return contract.TypeNotAcceptable
	default:
		if status >= http.StatusInternalServerError {
			return contract.TypeInternal
		}
		return contract.TypeBadRequest
	}
}

func truncateBody(body []byte, limit int) string {
	if limit <= 0 || len(body) <= limit {
		return string(body)
	}
	return string(body[:limit]) + "..."
}
