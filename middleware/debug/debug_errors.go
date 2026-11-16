package debug

import (
	"bufio"
	"bytes"
	"errors"
	"io"
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

// Middleware replaces eligible empty or plain-text error responses with structured JSON.
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
// Example response with HTTP status 404 and a request ID:
//
//	{
//	  "error": {
//	    "code": "not_found",
//	    "message": "not found",
//	    "category": "client",
//	    "type": "resource_not_found",
//	    "details": {
//	      "method": "GET",
//	      "path": "/api/missing",
//	      "hint": "Try /api/v1/users"
//	    }
//	  },
//	  "request_id": "req-123"
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
			if !rec.hijacked {
				defer rec.copyTrailers()
			}

			if rec.passthrough {
				return
			}

			if rec.status == 0 {
				rec.WriteHeader(http.StatusOK)
			}
			status := rec.status
			body := rec.body.Bytes()

			errorType := debugErrorType(status)
			if errorType == "" || !shouldReplaceError(rec.committed, body) {
				rec.flushTo()
				return
			}

			rec.discardReplacedRepresentationHeaders()
			internaltransport.ReplaceHeaders(w.Header(), rec.committed)
			_ = contract.WriteError(w, r, debugErrorPayload(errorType, status, r, cfg, body))
		})
	}
}

type debugErrorRecorder struct {
	dst         http.ResponseWriter
	header      http.Header
	committed   http.Header
	status      int
	body        bytes.Buffer
	maxBytes    int
	passthrough bool
	hijacked    bool
}

func newDebugErrorRecorder(dst http.ResponseWriter, maxBytes int) *debugErrorRecorder {
	header := make(http.Header)
	if dst != nil {
		internaltransport.CopyHeaders(header, dst.Header())
	}
	return &debugErrorRecorder{
		dst:      dst,
		header:   header,
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
	if r.status != 0 || r.hijacked {
		return
	}
	if status < 100 || status > 999 {
		panic("invalid WriteHeader code")
	}
	if status < 200 && status != http.StatusSwitchingProtocols {
		dst := r.dst.Header()
		original := dst.Clone()
		defer internaltransport.ReplaceHeaders(dst, original)
		internaltransport.ReplaceHeaders(dst, r.header)
		r.dst.WriteHeader(status)
		return
	}
	r.status = status
	r.committed = r.header.Clone()
	if r.passthrough || status == http.StatusSwitchingProtocols {
		r.passthrough = true
		r.flushHeaders()
	}
}

func (r *debugErrorRecorder) Write(p []byte) (int, error) {
	if r.status == 0 {
		r.WriteHeader(http.StatusOK)
	}
	if r.passthrough {
		return internaltransport.SafeWrite(r.dst, p)
	}
	if len(p) > 0 && (r.status == http.StatusNoContent || r.status == http.StatusNotModified) {
		return 0, http.ErrBodyNotAllowed
	}
	if r.maxBytes > 0 && r.body.Len()+len(p) > r.maxBytes {
		if err := r.commitPassthrough(); err != nil {
			return 0, err
		}
		return internaltransport.SafeWrite(r.dst, p)
	}
	return r.body.Write(p)
}

func (r *debugErrorRecorder) Flush() { _ = r.FlushError() }

func (r *debugErrorRecorder) FlushError() error {
	controller := http.NewResponseController(r.dst)
	if r.status != 0 || r.passthrough {
		if err := r.commitPassthrough(); err != nil {
			return err
		}
		return controller.Flush()
	}

	dst := r.dst.Header()
	original := dst.Clone()
	internaltransport.ReplaceHeaders(dst, r.header)
	internaltransport.EnsureNoSniff(dst)
	err := controller.Flush()
	if errors.Is(err, http.ErrNotSupported) {
		internaltransport.ReplaceHeaders(dst, original)
		return err
	}
	r.status = http.StatusOK
	r.committed = r.header.Clone()
	r.passthrough = true
	return err
}

func (r *debugErrorRecorder) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	if r.body.Len() > 0 || r.status != 0 {
		if err := r.commitPassthrough(); err != nil {
			return nil, nil, err
		}
	}
	conn, rw, err := http.NewResponseController(r.dst).Hijack()
	if err == nil {
		r.passthrough = true
		r.hijacked = true
	}
	return conn, rw, err
}

func (r *debugErrorRecorder) statusCode() int {
	if r.status == 0 {
		return http.StatusOK
	}
	return r.status
}

func (r *debugErrorRecorder) flushTo() {
	r.flushHeaders()
	_, _ = internaltransport.SafeWrite(r.dst, r.body.Bytes())
}

func (r *debugErrorRecorder) flushHeaders() {
	if r.dst == nil {
		return
	}
	if r.committed == nil {
		r.committed = r.header.Clone()
	}
	dst := r.dst.Header()
	internaltransport.ReplaceHeaders(dst, r.committed)
	internaltransport.EnsureNoSniff(dst)
	r.dst.WriteHeader(r.statusCode())
}

func (r *debugErrorRecorder) copyTrailers() {
	internaltransport.CopyResponseTrailers(r.dst.Header(), r.committed, r.header)
}

func (r *debugErrorRecorder) discardReplacedRepresentationHeaders() {
	remove := func(name string) bool {
		return name == "Content-Length" || name == "Etag" || internaltransport.IsRepresentationDigestField(name)
	}
	internaltransport.RemoveResponseFields(r.committed, remove)
	internaltransport.RemoveResponseFields(r.header, remove)
}

func (r *debugErrorRecorder) commitPassthrough() error {
	if r.passthrough {
		return nil
	}
	r.passthrough = true
	r.flushHeaders()
	if r.body.Len() == 0 {
		return nil
	}
	size := r.body.Len()
	n, err := internaltransport.SafeWrite(r.dst, r.body.Bytes())
	r.body.Reset()
	if err == nil && n != size {
		return io.ErrShortWrite
	}
	return err
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
	for _, line := range header.Values("Content-Encoding") {
		for encoding := range strings.SplitSeq(line, ",") {
			encoding = strings.TrimSpace(encoding)
			if encoding != "" && !strings.EqualFold(encoding, "identity") {
				return false
			}
		}
	}
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

func debugErrorPayload(errorType contract.ErrorType, status int, r *http.Request, cfg Config, body []byte) contract.APIError {
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
		Type(errorType).
		Code(code).
		Message(message).
		Details(details).
		Build()
}

func debugErrorType(status int) contract.ErrorType {
	switch status {
	case http.StatusBadRequest:
		return contract.TypeBadRequest
	case http.StatusUnauthorized:
		return contract.TypeUnauthorized
	case http.StatusForbidden:
		return contract.TypeForbidden
	case http.StatusNotFound:
		return contract.TypeNotFound
	case http.StatusMethodNotAllowed:
		return contract.TypeMethodNotAllowed
	case http.StatusNotAcceptable:
		return contract.TypeNotAcceptable
	case http.StatusRequestTimeout:
		return contract.TypeTimeout
	case http.StatusConflict:
		return contract.TypeConflict
	case http.StatusGone:
		return contract.TypeGone
	case http.StatusRequestEntityTooLarge:
		return contract.TypePayloadTooLarge
	case http.StatusUnprocessableEntity:
		return contract.TypeInvalidRequest
	case http.StatusTooManyRequests:
		return contract.TypeRateLimited
	case http.StatusInternalServerError:
		return contract.TypeInternal
	case http.StatusNotImplemented:
		return contract.TypeNotImplemented
	case http.StatusBadGateway:
		return contract.TypeBadGateway
	case http.StatusServiceUnavailable:
		return contract.TypeUnavailable
	case http.StatusGatewayTimeout:
		return contract.TypeGatewayTimeout
	default:
		return ""
	}
}

func truncateBody(body []byte, limit int) string {
	if limit <= 0 || len(body) <= limit {
		return string(body)
	}
	return string(body[:limit]) + "..."
}
