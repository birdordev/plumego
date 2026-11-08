package compression

import (
	"bufio"
	"compress/gzip"
	"net"
	"net/http"
	"strconv"
	"strings"

	internaltransport "github.com/birdordev/plumego/internal/httputil"
	"github.com/birdordev/plumego/middleware"
)

// Middleware compresses HTTP responses when the client supports it via
// Accept-Encoding.
// It intelligently skips compression for:
//   - WebSocket upgrades
//   - Server-Sent Events (SSE)
//   - Already compressed content
//   - Binary content (images, videos, etc.)
//   - Responses that exceed MaxBufferBytes before gzip output starts
//
// Example:
//
//	import "github.com/birdordev/plumego/middleware/compression"
//
//	// Use explicit configuration
//	config := compression.Config{
//		MaxBufferBytes: 5 << 20, // 5MB max buffer
//	}
//	handler := compression.Middleware(config)(myHandler)
//
// The middleware adds the following headers:
//   - Content-Encoding: gzip (when compression is used)
//   - Vary: Accept-Encoding (to indicate response varies by encoding)
//
// Config controls gzip compression middleware behavior.
//
// Example:
//
//	import "github.com/birdordev/plumego/middleware/compression"
//
//	config := compression.Config{
//		MaxBufferBytes: 5 << 20, // 5MB max buffer
//	}
//	handler := compression.Middleware(config)(myHandler)
type Config struct {
	// MaxBufferBytes is the maximum response size to buffer for compression.
	// Responses larger than this bypass compression only if the limit is reached
	// before gzip output starts. Once gzip output has started, the response keeps
	// streaming through the gzip writer.
	// Default: 10MB (10 << 20)
	MaxBufferBytes int
}

// DefaultConfig returns default gzip compression settings.
func DefaultConfig() Config {
	return Config{MaxBufferBytes: 10 << 20}
}

var compressedContentTypePrefixes = []string{
	"image/",
	"video/",
	"audio/",
	"application/zip",
	"application/gzip",
	"application/pdf",
	"application/octet-stream",
}

// Middleware creates gzip compression middleware with explicit configuration.
func Middleware(cfg Config) middleware.Middleware {
	if cfg.MaxBufferBytes <= 0 {
		cfg.MaxBufferBytes = DefaultConfig().MaxBufferBytes
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Skip if client doesn't support gzip
			if !acceptsGzip(r.Header.Get("Accept-Encoding")) {
				next.ServeHTTP(w, r)
				return
			}

			// Skip WebSocket upgrades
			connection := strings.ToLower(r.Header.Get("Connection"))
			if strings.Contains(connection, "upgrade") || r.Header.Get("Upgrade") != "" {
				next.ServeHTTP(w, r)
				return
			}

			// Skip SSE (Server-Sent Events)
			accept := r.Header.Get("Accept")
			if strings.Contains(accept, "text/event-stream") {
				next.ServeHTTP(w, r)
				return
			}

			// Create wrapper
			gw := &gzipResponseWriter{
				ResponseWriter: w,
				cfg:            cfg,
			}

			defer func() {
				if rec := recover(); rec != nil {
					gw.finalize(true)
					panic(rec)
				}
				gw.finalize(false)
			}()

			next.ServeHTTP(gw, r)
		})
	}
}

func acceptsGzip(header string) bool {
	var (
		gzipQ       float64
		wildcardQ   float64
		hasGzip     bool
		hasWildcard bool
	)

	for _, part := range strings.Split(header, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}

		segments := strings.Split(part, ";")
		coding := strings.ToLower(strings.TrimSpace(segments[0]))
		q := 1.0
		for _, param := range segments[1:] {
			name, value, ok := strings.Cut(strings.TrimSpace(param), "=")
			if !ok || !strings.EqualFold(strings.TrimSpace(name), "q") {
				continue
			}
			parsed, err := strconv.ParseFloat(strings.TrimSpace(value), 64)
			if err != nil {
				q = 0
				break
			}
			q = parsed
			break
		}

		switch coding {
		case "gzip":
			gzipQ = q
			hasGzip = true
		case "*":
			wildcardQ = q
			hasWildcard = true
		}
	}

	if hasGzip {
		return gzipQ > 0
	}
	return hasWildcard && wildcardQ > 0
}

type gzipResponseWriter struct {
	http.ResponseWriter
	cfg             Config
	gz              *gzip.Writer
	compressionUsed bool
	wroteHeader     bool
	buffer          *internaltransport.BufferedResponse
	headersFlushed  bool
	hijacked        bool
	passThrough     bool
	pendingCompress bool
}

func (w *gzipResponseWriter) Unwrap() http.ResponseWriter {
	return w.ResponseWriter
}

func (w *gzipResponseWriter) finalize(panicking bool) {
	if w.hijacked {
		return
	}
	if panicking && !w.headersFlushed && w.gz == nil {
		return
	}
	if !w.wroteHeader && w.buffer != nil {
		// Header-only handlers still own a final implicit 200 response.
		w.WriteHeader(http.StatusOK)
	}
	if w.pendingCompress && (w.buffer == nil || w.buffer.Len() == 0) {
		w.pendingCompress = false
		w.compressionUsed = false
		w.flushHeaders()
		return
	}
	if !w.compressionUsed {
		if w.pendingCompress {
			w.resolvePendingCompression()
		}
		if w.headersFlushed {
			return
		}
		return
	}
	if w.gz != nil {
		// Close flushes the gzip footer to the underlying ResponseWriter. The
		// response is already committed at this point, so a write failure cannot
		// be retried or surfaced to the caller; discard it the same way the
		// SafeWrite paths above do.
		_ = w.gz.Close()
		return
	}
	w.flushHeaders()
	if w.buffer != nil && w.buffer.Len() > 0 {
		_, _ = internaltransport.SafeWrite(w.ResponseWriter, w.buffer.Body())
	}
}

func (w *gzipResponseWriter) Header() http.Header {
	if w.passThrough && w.buffer == nil {
		return w.ResponseWriter.Header()
	}
	w.ensureBuffer()
	return w.buffer.Header()
}

func (w *gzipResponseWriter) WriteHeader(statusCode int) {
	if statusCode >= 100 && statusCode < 200 && statusCode != http.StatusSwitchingProtocols {
		if !w.wroteHeader {
			// Forward a snapshot without leaving interim-only values in the
			// underlying header map, which later final commits overlay.
			dst := w.ResponseWriter.Header()
			original := dst.Clone()
			defer internaltransport.ReplaceHeaders(dst, original)
			internaltransport.CopyHeaders(dst, w.Header())
			w.ResponseWriter.WriteHeader(statusCode)
		}
		return
	}
	if w.wroteHeader {
		return
	}
	w.wroteHeader = true

	if w.passThrough {
		w.compressionUsed = false
		w.ResponseWriter.WriteHeader(statusCode)
		w.headersFlushed = true
		return
	}

	w.ensureBuffer()
	w.buffer.WriteHeader(statusCode)

	if w.shouldDeferCompressionDecision(statusCode) {
		w.pendingCompress = true
		return
	}

	if !w.shouldCompress(statusCode) {
		w.compressionUsed = false
		w.flushHeaders()
		return
	}

	// Start buffering
	w.compressionUsed = true
}

func (w *gzipResponseWriter) Write(p []byte) (int, error) {
	if w.passThrough {
		return internaltransport.SafeWrite(w.ResponseWriter, p)
	}

	if !w.wroteHeader {
		if len(p) > 0 && w.Header().Get("Content-Type") == "" {
			w.Header().Set("Content-Type", http.DetectContentType(p))
		}
		w.WriteHeader(http.StatusOK)
	}
	if w.pendingCompress {
		if len(p) > 0 && w.Header().Get("Content-Type") == "" {
			w.Header().Set("Content-Type", http.DetectContentType(p))
		}
		w.resolvePendingCompression()
	}

	// If not compressing, write directly
	if !w.compressionUsed {
		w.flushHeaders()
		return internaltransport.SafeWrite(w.ResponseWriter, p)
	}

	// Buffer the data
	w.ensureBuffer()

	// Once gzip output has started, continue writing through the gzip stream.
	// Switching back to an uncompressed response after headers were flushed
	// would produce a corrupt response.
	if w.gz != nil {
		return w.gz.Write(p)
	}

	// If buffer exceeds max, switch to bypass mode
	if w.cfg.MaxBufferBytes > 0 && w.buffer.Len()+len(p) > w.cfg.MaxBufferBytes {
		// Write buffered data as-is (no compression)
		w.compressionUsed = false

		// Remove compression headers
		w.buffer.Header().Del("Content-Encoding")
		w.flushHeaders()

		// Write buffered data in chunks to avoid memory spikes
		const chunkSize = 8192 // 8KB chunks
		totalWritten := 0
		body := w.buffer.Body()
		for totalWritten < len(body) {
			end := totalWritten + chunkSize
			if end > len(body) {
				end = len(body)
			}
			n, err := internaltransport.SafeWrite(w.ResponseWriter, body[totalWritten:end])
			if err != nil {
				w.buffer.ClearBody()
				return totalWritten, err
			}
			totalWritten += n
		}

		w.buffer.ClearBody()
		n, err := internaltransport.SafeWrite(w.ResponseWriter, p)
		return totalWritten + n, err
	}

	_, err := w.buffer.Write(p)
	if err != nil {
		return 0, err
	}

	// If we have enough data, start compressing
	if w.gz == nil && w.buffer.Len() > 0 {
		// Update headers for compression
		w.buffer.Header().Del("Content-Length")
		internaltransport.AddVary(w.buffer.Header(), "Accept-Encoding")
		w.buffer.Header().Set("Content-Encoding", "gzip")
		w.flushHeaders()
		w.gz = gzip.NewWriter(w.ResponseWriter)

		// Write buffered data
		if _, err := w.gz.Write(w.buffer.Body()); err != nil {
			w.buffer.ClearBody()
			return 0, err
		}

		// Clear buffer
		w.buffer.ClearBody()
		return len(p), nil
	}

	return len(p), nil
}

func (w *gzipResponseWriter) shouldDeferCompressionDecision(statusCode int) bool {
	if statusCode >= 400 {
		return false
	}
	if w.Header().Get("Content-Encoding") != "" {
		return false
	}
	return w.Header().Get("Content-Type") == ""
}

func (w *gzipResponseWriter) resolvePendingCompression() {
	if !w.pendingCompress {
		return
	}
	w.pendingCompress = false
	w.compressionUsed = w.shouldCompress(w.buffer.StatusCode())
	if !w.compressionUsed {
		w.flushHeaders()
	}
}

func (w *gzipResponseWriter) shouldCompress(statusCode int) bool {
	// Don't compress error responses
	if statusCode >= 400 {
		return false
	}

	// Check content type
	contentType := w.Header().Get("Content-Type")
	if contentType == "" {
		return false
	}

	// Skip already compressed content
	if w.Header().Get("Content-Encoding") != "" {
		return false
	}

	// Skip streaming content types
	lowerContentType := strings.ToLower(contentType)
	if strings.Contains(lowerContentType, "stream") ||
		strings.Contains(lowerContentType, "event-stream") ||
		strings.Contains(lowerContentType, "websocket") {
		return false
	}

	// Skip binary content
	for _, ct := range compressedContentTypePrefixes {
		if strings.HasPrefix(lowerContentType, ct) {
			return false
		}
	}

	return true
}

func (w *gzipResponseWriter) ensureBuffer() {
	if w.buffer == nil {
		w.buffer = internaltransport.NewBufferedResponse(w.cfg.MaxBufferBytes)
	}
}

func (w *gzipResponseWriter) flushHeaders() {
	if w.buffer == nil || w.ResponseWriter == nil {
		return
	}
	if w.headersFlushed {
		return
	}
	internaltransport.CommitHeadersCopy(w.ResponseWriter, w.buffer.Header(), w.buffer.StatusCode())
	w.headersFlushed = true
}

func (w *gzipResponseWriter) Flush() {
	if !w.compressionUsed {
		w.commitPassThrough()
	}
	if w.gz != nil {
		_ = w.gz.Flush()
	}
	internaltransport.FlushIfSupported(w.ResponseWriter)
}

func (w *gzipResponseWriter) commitPassThrough() {
	w.passThrough = true
	w.compressionUsed = false
	w.wroteHeader = true
	if w.headersFlushed {
		return
	}
	if w.buffer != nil {
		w.flushHeaders()
		return
	}
	w.ResponseWriter.WriteHeader(http.StatusOK)
	w.headersFlushed = true
}

func (w *gzipResponseWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	if w.gz != nil || w.headersFlushed {
		return nil, nil, http.ErrNotSupported
	}
	conn, rw, err := internaltransport.HijackIfSupported(w.ResponseWriter)
	if err != nil {
		return nil, nil, err
	}
	w.hijacked = true
	w.headersFlushed = true
	w.wroteHeader = true
	w.compressionUsed = false
	return conn, rw, nil
}
