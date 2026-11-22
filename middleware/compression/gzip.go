package compression

import (
	"bufio"
	"compress/gzip"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"

	internaltransport "github.com/birdordev/plumego/internal/httputil"
	"github.com/birdordev/plumego/middleware"
)

// Config controls gzip compression middleware behavior.
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

// Middleware compresses eligible HTTP responses when the client accepts gzip.
// It bypasses negotiation for upgrade requests and requests accepting
// Server-Sent Events. It skips compression for already encoded responses,
// streaming or common binary content, range responses, error responses,
// status 204 or 304, and responses that exceed [Config.MaxBufferBytes] before
// gzip output starts.
//
// Negotiated gzip and identity responses include Vary: Accept-Encoding and
// preserve other Vary values. The downstream handler can read and edit response
// headers supplied by outer middleware before commitment. Compressed responses have
// Content-Encoding: gzip. HEAD responses without a body may omit length
// and validators when the representation depends on compressed output.
//
// Example:
//
//	import "github.com/birdordev/plumego/middleware/compression"
//
//	config := compression.Config{MaxBufferBytes: 5 << 20}
//	handler := compression.Middleware(config)(myHandler)
func Middleware(cfg Config) middleware.Middleware {
	if cfg.MaxBufferBytes <= 0 {
		cfg.MaxBufferBytes = DefaultConfig().MaxBufferBytes
	}

	compressors := make(chan *gzipStream, 4)
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Skip WebSocket upgrades
			connection := strings.ToLower(r.Header.Get("Connection"))
			if strings.Contains(connection, "upgrade") || r.Header.Get("Upgrade") != "" {
				next.ServeHTTP(w, r)
				return
			}

			variant := internaltransport.NewVaryWriter(w, "Accept-Encoding", "Accept")
			accept := r.Header.Get("Accept")
			if strings.Contains(accept, "text/event-stream") ||
				!acceptsGzip(strings.Join(r.Header.Values("Accept-Encoding"), ",")) {
				next.ServeHTTP(variant, r)
				variant.Prepare()
				return
			}

			// Create wrapper
			gw := &gzipResponseWriter{
				ResponseWriter: variant,
				cfg:            cfg,
				headRequest:    r.Method == http.MethodHead,
				compressors:    compressors,
			}

			returned := false
			defer func() {
				gw.finalize(!returned)
				if returned {
					variant.Prepare()
				}
			}()

			next.ServeHTTP(gw, r)
			returned = true
		})
	}
}

func acceptsGzip(header string) bool {
	var (
		gzipAccepted     bool
		wildcardAccepted bool
		hasGzip          bool
	)

	for part := range strings.SplitSeq(header, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}

		coding, params, _ := strings.Cut(part, ";")
		coding = strings.ToLower(strings.TrimSpace(coding))
		accepted := true
		if params != "" {
			for param := range strings.SplitSeq(params, ";") {
				name, value, ok := strings.Cut(strings.TrimSpace(param), "=")
				if !strings.EqualFold(strings.TrimSpace(name), "q") {
					continue
				}
				accepted = ok && positiveQualityWeight(strings.TrimSpace(value))
				break
			}
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

// gzipStream lets an idle compressor release its request writer without
// resetting the compressor until its next use.
type gzipStream struct {
	*gzip.Writer
	output gzipOutput
}

type gzipOutput struct{ dst io.Writer }

func (o *gzipOutput) Write(p []byte) (int, error) { return o.dst.Write(p) }

func (w *gzipResponseWriter) startGzip() {
	select {
	case w.gz = <-w.compressors:
		w.gz.output.dst = w.ResponseWriter
		w.gz.Reset(&w.gz.output)
	default:
		w.gz = &gzipStream{output: gzipOutput{dst: w.ResponseWriter}}
		w.gz.Writer = gzip.NewWriter(&w.gz.output)
	}
}

func (w *gzipResponseWriter) releaseGzip() {
	w.gz.output.dst = nil
	select {
	case w.compressors <- w.gz:
	default:
	}
}

type gzipResponseWriter struct {
	http.ResponseWriter
	cfg             Config
	headRequest     bool
	gz              *gzipStream
	compressors     chan *gzipStream
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

func (w *gzipResponseWriter) finalize(aborted bool) {
	if w.hijacked {
		return
	}
	if aborted && !w.headersFlushed && w.gz == nil {
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
	if w.headRequest && w.buffer != nil && !w.headersFlushed && w.gz == nil && w.shouldCompress(w.buffer.StatusCode()) {
		// A router can suppress the body before it reaches gzip. The identity
		// length and validators do not describe the negotiated representation.
		w.buffer.FinalHeader().Del("Content-Length")
		removeUncompressedMetadata(w.buffer.FinalHeader())
		removeUncompressedMetadata(w.buffer.Header())
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
		defer w.releaseGzip()
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
		internaltransport.ReplaceHeaders(dst, w.Header())
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

	if w.gz != nil {
		return w.gz.Write(p)
	}
	if len(p) > w.cfg.MaxBufferBytes {
		w.compressionUsed = false
		w.flushHeaders()
		return internaltransport.SafeWrite(w.ResponseWriter, p)
	}
	if len(p) == 0 {
		return 0, nil
	}

	header := w.buffer.FinalHeader()
	removeUncompressedMetadata(header)
	header.Del("Content-Length")
	header.Set("Content-Encoding", "gzip")
	w.flushHeaders()
	w.startGzip()
	if _, err := w.gz.Write(p); err != nil {
		return 0, err
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
		internaltransport.CopyHeaders(w.buffer.Header(), w.ResponseWriter.Header())
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
	header := w.ResponseWriter.Header()
	internaltransport.ReplaceHeaders(header, w.buffer.FinalHeader())
	internaltransport.EnsureNoSniff(header)
	w.ResponseWriter.WriteHeader(w.buffer.StatusCode())
	w.headersFlushed = true
}

func (w *gzipResponseWriter) Flush() { _ = w.FlushError() }

func (w *gzipResponseWriter) FlushError() error {
	if !w.wroteHeader && !w.headersFlushed && w.gz == nil {
		return w.flushImplicitResponse()
	}
	if w.gz == nil {
		if !w.headersFlushed && !hasFlushCapability(w.ResponseWriter) {
			return http.ErrNotSupported
		}
		w.commitPassThrough()
	} else if err := w.gz.Flush(); err != nil {
		return err
	}
	return http.NewResponseController(w.ResponseWriter).Flush()
}

func hasFlushCapability(dst http.ResponseWriter) bool {
	for {
		switch writer := dst.(type) {
		case *internaltransport.VaryWriter:
			dst = writer.Unwrap()
		case interface{ FlushError() error }, http.Flusher:
			return true
		case interface{ Unwrap() http.ResponseWriter }:
			dst = writer.Unwrap()
		default:
			return false
		}
	}
}

func (w *gzipResponseWriter) flushImplicitResponse() error {
	dst := w.ResponseWriter.Header()
	original := dst.Clone()
	if w.buffer != nil {
		internaltransport.ReplaceHeaders(dst, w.buffer.Header())
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
