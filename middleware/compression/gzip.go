package compression

import (
	"bufio"
	"compress/gzip"
	"errors"
	"net"
	"net/http"
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

			variant := internaltransport.NewVaryWriter(w, "Accept-Encoding")
			if !acceptsGzip(strings.Join(r.Header.Values("Accept-Encoding"), ",")) {
				next.ServeHTTP(variant, r)
				variant.Prepare()
				return
			}

			// Create wrapper
			gw := &gzipResponseWriter{
				ResponseWriter: variant,
				cfg:            cfg,
			}

			defer func() {
				if rec := recover(); rec != nil {
					gw.finalize(true)
					panic(rec)
				}
				gw.finalize(false)
				variant.Prepare()
			}()

			next.ServeHTTP(gw, r)
		})
	}
}

func acceptsGzip(header string) bool {
	var (
		gzipAccepted     bool
		wildcardAccepted bool
		hasGzip          bool
	)

	for _, part := range strings.Split(header, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}

		segments := strings.Split(part, ";")
		coding := strings.ToLower(strings.TrimSpace(segments[0]))
		accepted := true
		for _, param := range segments[1:] {
			name, value, ok := strings.Cut(strings.TrimSpace(param), "=")
			if !strings.EqualFold(strings.TrimSpace(name), "q") {
				continue
			}
			accepted = ok && positiveQualityWeight(strings.TrimSpace(value))
			break
		}

		switch coding {
		case "gzip":
			gzipAccepted = accepted
			hasGzip = true
		case "*":
			wildcardAccepted = accepted
		}
	}

	if hasGzip {
		return gzipAccepted
	}
	return wildcardAccepted
}

// positiveQualityWeight accepts nonzero HTTP qvalues (RFC 9110, section 12.4.2).
func positiveQualityWeight(value string) bool {
	whole, fraction, _ := strings.Cut(value, ".")
	if (whole != "0" && whole != "1") || len(fraction) > 3 {
		return false
	}
	positive := whole == "1"
	for i := range len(fraction) {
		digit := fraction[i]
		if digit < '0' || digit > '9' || (whole == "1" && digit != '0') {
			return false
		}
		positive = positive || digit != '0'
	}
	return positive
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
	if w.buffer != nil {
		defer func() {
			if w.gz != nil {
				removeUncompressedMetadata(w.buffer.Header())
			}
			w.buffer.CopyTrailers(w.ResponseWriter.Header())
		}()
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
	if w.wroteHeader {
		return
	}
	if statusCode < 100 || statusCode > 999 {
		panic("invalid WriteHeader code")
	}
	if statusCode < 200 && statusCode != http.StatusSwitchingProtocols {
		// Forward a snapshot without leaving interim-only values in the
		// underlying header map, which later final commits overlay.
		dst := w.ResponseWriter.Header()
		original := dst.Clone()
		defer internaltransport.ReplaceHeaders(dst, original)
		internaltransport.CopyHeaders(dst, w.Header())
		w.ResponseWriter.WriteHeader(statusCode)
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
		sniffContentType(w.Header(), p)
		w.WriteHeader(http.StatusOK)
	}
	if w.pendingCompress {
		sniffContentType(w.buffer.FinalHeader(), p)
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
		header := w.buffer.FinalHeader()
		removeUncompressedMetadata(header)
		header.Del("Content-Length")
		header.Set("Content-Encoding", "gzip")
		dst := w.ResponseWriter.Header()
		removeUncompressedMetadata(dst)
		dst.Del("Content-Length")
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

func removeUncompressedMetadata(header http.Header) {
	internaltransport.RemoveResponseFields(header, internaltransport.IsRepresentationDigestField)
	// Keep ETag declarations so a weak tag supplied after commitment can
	// still be forwarded. Strong tags are tied to the uncompressed bytes.
	for key, values := range header {
		if http.CanonicalHeaderKey(strings.TrimPrefix(key, http.TrailerPrefix)) == "Etag" &&
			(len(values) != 1 || !strings.HasPrefix(strings.TrimSpace(values[0]), "W/")) {
			delete(header, key)
		}
	}
}

func sniffContentType(header http.Header, p []byte) {
	_, hasContentType := header["Content-Type"]
	if len(p) > 0 && !hasContentType && header.Get("Content-Encoding") == "" && header.Get("Transfer-Encoding") == "" {
		header.Set("Content-Type", http.DetectContentType(p))
	}
}

func (w *gzipResponseWriter) shouldDeferCompressionDecision(statusCode int) bool {
	if !canCompressStatus(statusCode) {
		return false
	}
	header := w.buffer.FinalHeader()
	if header.Get("Content-Encoding") != "" {
		return false
	}
	_, hasContentType := header["Content-Type"]
	return !hasContentType
}

func canCompressStatus(statusCode int) bool {
	return statusCode < http.StatusBadRequest &&
		statusCode != http.StatusNoContent && statusCode != http.StatusNotModified
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
	header := w.buffer.FinalHeader()
	if !canCompressStatus(statusCode) {
		return false
	}
	// Byte ranges describe the original representation.
	if statusCode == http.StatusPartialContent || header.Get("Content-Range") != "" {
		return false
	}

	// Check content type
	contentType := header.Get("Content-Type")
	if contentType == "" {
		return false
	}

	// Skip already compressed content
	if header.Get("Content-Encoding") != "" {
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
	w.buffer.WriteHeader(w.buffer.StatusCode())
	internaltransport.CommitHeadersCopy(w.ResponseWriter, w.buffer.FinalHeader(), w.buffer.StatusCode())
	w.headersFlushed = true
}

func (w *gzipResponseWriter) Flush() { _ = w.FlushError() }

func (w *gzipResponseWriter) FlushError() error {
	if !w.wroteHeader && !w.headersFlushed && w.gz == nil {
		return w.flushImplicitResponse()
	}
	if w.gz == nil {
		w.commitPassThrough()
	} else if err := w.gz.Flush(); err != nil {
		return err
	}
	return http.NewResponseController(w.ResponseWriter).Flush()
}

func (w *gzipResponseWriter) flushImplicitResponse() error {
	dst := w.ResponseWriter.Header()
	original := dst.Clone()
	if w.buffer != nil {
		internaltransport.CopyHeaders(dst, w.buffer.Header())
		internaltransport.EnsureNoSniff(dst)
	}
	err := http.NewResponseController(w.ResponseWriter).Flush()
	if errors.Is(err, http.ErrNotSupported) {
		internaltransport.ReplaceHeaders(dst, original)
		return err
	}
	if w.buffer != nil {
		w.buffer.WriteHeader(http.StatusOK)
	}
	w.passThrough = true
	w.wroteHeader = true
	w.headersFlushed = true
	return err
}

func (w *gzipResponseWriter) commitPassThrough() {
	w.passThrough = true
	w.pendingCompress = false
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
