package httputil

import (
	"bufio"
	"bytes"
	"context"
	"github.com/spcent/plumego/contract"
	"net"
	"net/http"
	"strings"
	"sync/atomic"
)

const (
	HeaderContentTypeNoSniff = "X-Content-Type-Options"
	ContentTypeNoSniffValue  = "nosniff"
	HeaderForwardedFor       = "X-Forwarded-For"
	HeaderRealIP             = "X-Real-IP"
)

// EnsureNoSniff sets X-Content-Type-Options to nosniff if it is not present.
// This reduces XSS risk from MIME sniffing when responses contain user-controlled text.
func EnsureNoSniff(header http.Header) {
	if header == nil {
		return
	}
	if header.Get(HeaderContentTypeNoSniff) == "" {
		header.Set(HeaderContentTypeNoSniff, ContentTypeNoSniffValue)
	}
}

// SafeWrite writes response bytes after applying minimal response hardening.
//
// SECURITY: This helper is intended for middleware and infrastructure code that
// needs to copy an already-constructed HTTP response. It does NOT perform any
// context-aware HTML/JS escaping. Callers that generate HTML or other active
// content MUST apply appropriate escaping/encoding before passing data here,
// and must set an appropriate Content-Type header (for example, application/json).
//
// The only protection applied here is X-Content-Type-Options: nosniff to
// prevent browsers from MIME-sniffing non-HTML responses as HTML.
//
// codeql[go/reflected-xss]: SafeWrite is a low-level passthrough used by
// response caching/middleware; it is not responsible for HTML encoding.
func SafeWrite(w http.ResponseWriter, body []byte) (int, error) {
	if w == nil {
		return 0, nil
	}
	EnsureNoSniff(w.Header())
	return w.Write(body)
}

// ResponseRecorder captures response data while still writing to the underlying writer.
type ResponseRecorder struct {
	route atomic.Pointer[contract.RequestContext]
	http.ResponseWriter
	statusCode   int
	header       http.Header
	body         *bytes.Buffer
	bytesWritten int
	written      bool
}

// NewResponseRecorder creates a response recorder with sane defaults.
func NewResponseRecorder(w http.ResponseWriter) *ResponseRecorder {
	return &ResponseRecorder{
		ResponseWriter: w,
		statusCode:     http.StatusOK,
		header:         make(http.Header),
		body:           &bytes.Buffer{},
	}
}

// Unwrap returns the underlying response writer for http.ResponseController.
func (r *ResponseRecorder) Unwrap() http.ResponseWriter {
	return r.ResponseWriter
}

// Header returns the recorded header map.
func (r *ResponseRecorder) Header() http.Header {
	return r.header
}

// WriteHeader records the status code and writes headers to the underlying writer once.
func (r *ResponseRecorder) WriteHeader(code int) {
	if r.written {
		return
	}
	r.statusCode = code
	r.written = true

	CopyHeaders(r.ResponseWriter.Header(), r.header)
	EnsureNoSniff(r.ResponseWriter.Header())
	r.ResponseWriter.WriteHeader(code)
}

// Write captures body bytes and writes through to the underlying writer.
//
// SECURITY NOTE: This is infrastructure that captures response data for
// middleware, metrics, logging, and caching. It does not inject user input into
// HTML contexts; active content escaping belongs in handlers that generate it.
func (r *ResponseRecorder) Write(b []byte) (int, error) {
	if !r.written {
		r.WriteHeader(http.StatusOK)
	}

	r.body.Write(b)
	n, err := SafeWrite(r.ResponseWriter, b)
	r.bytesWritten += n
	return n, err
}

// StatusCode returns the recorded status code.
func (r *ResponseRecorder) StatusCode() int {
	if r.statusCode == 0 {
		return http.StatusOK
	}
	return r.statusCode
}

// Body returns the captured response body bytes.
func (r *ResponseRecorder) Body() []byte {
	return r.body.Bytes()
}

// BytesWritten returns the number of body bytes successfully written through.
func (r *ResponseRecorder) BytesWritten() int {
	return r.bytesWritten
}

// Hijack forwards http.Hijacker when the wrapped writer supports it.
func (r *ResponseRecorder) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	hj, ok := r.ResponseWriter.(http.Hijacker)
	if !ok {
		return nil, nil, http.ErrNotSupported
	}
	return hj.Hijack()
}

// Flush forwards http.Flusher when the wrapped writer supports it.
func (r *ResponseRecorder) Flush() {
	if fl, ok := r.ResponseWriter.(http.Flusher); ok {
		fl.Flush()
	}
}

// FlushIfSupported forwards Flush to the underlying writer when available.
func FlushIfSupported(w http.ResponseWriter) {
	if flusher, ok := w.(http.Flusher); ok {
		flusher.Flush()
	}
}

// HijackIfSupported forwards Hijack to the underlying writer when available.
func HijackIfSupported(w http.ResponseWriter) (net.Conn, *bufio.ReadWriter, error) {
	hijacker, ok := w.(http.Hijacker)
	if !ok {
		return nil, nil, http.ErrNotSupported
	}
	return hijacker.Hijack()
}

// CommitHeadersCopy overlays source headers onto destination headers, ensures
// nosniff, then writes status.
func CommitHeadersCopy(dst http.ResponseWriter, src http.Header, status int) {
	if dst == nil {
		return
	}
	CopyHeaders(dst.Header(), src)
	EnsureNoSniff(dst.Header())
	dst.WriteHeader(status)
}

// AddVary appends Vary header tokens without duplicating existing values.
func AddVary(header http.Header, values ...string) {
	if header == nil {
		return
	}

	existing := map[string]struct{}{}
	for _, value := range header.Values("Vary") {
		for _, token := range strings.Split(value, ",") {
			token = strings.TrimSpace(token)
			if token == "" {
				continue
			}
			existing[strings.ToLower(token)] = struct{}{}
		}
	}

	for _, value := range values {
		for _, token := range strings.Split(value, ",") {
			token = strings.TrimSpace(token)
			if token == "" {
				continue
			}
			key := strings.ToLower(token)
			if _, ok := existing[key]; ok {
				continue
			}
			header.Add("Vary", token)
			existing[key] = struct{}{}
		}
	}
}

// CopyHeaders overlays cloned source values onto destination headers.
// Destination keys absent from src are preserved.
func CopyHeaders(dst, src http.Header) {
	if dst == nil || src == nil {
		return
	}
	for key, values := range src {
		cloned := make([]string, len(values))
		copy(cloned, values)
		dst[key] = cloned
	}
}

// ReplaceHeaders replaces the complete destination header map with cloned
// source values. Destination keys absent from src are removed.
func ReplaceHeaders(dst, src http.Header) {
	if dst == nil {
		return
	}
	for key := range dst {
		delete(dst, key)
	}
	CopyHeaders(dst, src)
}

func ClientIP(r *http.Request) string {
	if r == nil {
		return ""
	}

	// Prefer the first syntactically valid IP from X-Forwarded-For, then
	// X-Real-IP. Validating with net.ParseIP avoids returning spoofed or
	// malformed header values verbatim.
	for _, part := range strings.Split(r.Header.Get(HeaderForwardedFor), ",") {
		if ip := validIP(part); ip != "" {
			return ip
		}
	}
	if ip := validIP(r.Header.Get(HeaderRealIP)); ip != "" {
		return ip
	}

	return DirectClientIP(r)
}

// validIP trims value and returns it only when it parses as an IP address.
func validIP(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	if net.ParseIP(value) != nil {
		return value
	}
	return ""
}

// DirectClientIP extracts the peer IP from RemoteAddr only.
//
// Use this for security-sensitive defaults such as rate limiting when the
// application has not explicitly configured trusted proxy handling.
func DirectClientIP(r *http.Request) string {
	if r == nil {
		return ""
	}

	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err == nil {
		return host
	}

	return strings.TrimSpace(r.RemoteAddr)
}

// PublishRouteContext visits transport wrappers without accessing response headers.
// Receivers must synchronize publication and forward to any outer observers.
func PublishRouteContext(w http.ResponseWriter, ctx context.Context) {
	for w != nil {
		if receiver, ok := w.(interface{ SetRouteContext(context.Context) }); ok {
			receiver.SetRouteContext(ctx)
			return
		}
		wrapper, ok := w.(interface{ Unwrap() http.ResponseWriter })
		if !ok {
			return
		}
		w = wrapper.Unwrap()
	}
}

// SetRouteContext publishes only immutable route identity, including after a timeout.
func (r *ResponseRecorder) SetRouteContext(ctx context.Context) {
	rc := contract.RequestContextFromContext(ctx)
	rc.Params = nil
	r.route.Store(&rc)
	PublishRouteContext(r.ResponseWriter, ctx)
}

// RouteContext returns the latest published route identity without sharing mutable data.
func (r *ResponseRecorder) RouteContext() contract.RequestContext {
	if rc := r.route.Load(); rc != nil {
		return *rc
	}
	return contract.RequestContext{}
}
