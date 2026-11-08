package bodylimit

import (
	"bufio"
	"errors"
	"io"
	"net"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/spcent/plumego/contract"
	internaltransport "github.com/spcent/plumego/internal/httputil"
	"github.com/spcent/plumego/log"
	mw "github.com/spcent/plumego/middleware"
	internaltelemetry "github.com/spcent/plumego/middleware/internal/telemetry"
)

var errRequestTooLarge = errors.New("request body too large")

// Config controls request body limiting behavior.
type Config struct {
	// MaxBytes is the maximum request body size. Values less than or equal to
	// zero disable the middleware.
	MaxBytes int64

	// Logger receives redacted overrun events when configured.
	Logger log.StructuredLogger
}

// DefaultConfig returns a body limit config with the configured maximum size.
func DefaultConfig(maxBytes int64, logger log.StructuredLogger) Config {
	return Config{
		MaxBytes: maxBytes,
		Logger:   logger,
	}
}

// Middleware enforces a maximum request body size using a named configuration
// object.
func Middleware(config Config) mw.Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if config.MaxBytes <= 0 {
				next.ServeHTTP(w, r)
				return
			}

			bw := &bodyLimitResponseWriter{ResponseWriter: w}
			limited := &limitedBodyReader{
				w:        bw,
				req:      r,
				r:        r.Body,
				maxBytes: config.MaxBytes,
				logger:   config.Logger,
				now:      time.Now,
			}
			r.Body = limited

			defer bw.checkLimitError()
			next.ServeHTTP(bw, r)
		})
	}
}

type bodyLimitResponseWriter struct {
	http.ResponseWriter
	started       bool
	blocked       bool
	errorResponse bool
	pending       atomic.Pointer[bodyLimitError]
}

type bodyLimitError struct {
	request   *http.Request
	maxBytes  int64
	seenBytes int64
	at        time.Time
}

func (w *bodyLimitResponseWriter) Unwrap() http.ResponseWriter {
	return w.ResponseWriter
}

func (w *bodyLimitResponseWriter) WriteHeader(statusCode int) {
	w.checkLimitError()
	if w.blocked {
		return
	}
	if statusCode >= 200 || statusCode == http.StatusSwitchingProtocols {
		w.started = true
		w.errorResponse = statusCode >= http.StatusBadRequest
	}
	w.ResponseWriter.WriteHeader(statusCode)
}

func (w *bodyLimitResponseWriter) Write(p []byte) (int, error) {
	w.checkLimitError()
	if w.blocked {
		return len(p), nil
	}
	w.started = true
	return w.ResponseWriter.Write(p)
}

func (w *bodyLimitResponseWriter) Flush() {
	w.checkLimitError()
	if w.blocked {
		return
	}
	if flusher, ok := w.ResponseWriter.(http.Flusher); ok {
		w.started = true
		flusher.Flush()
	}
}

func (w *bodyLimitResponseWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	w.checkLimitError()
	if w.blocked {
		return nil, nil, http.ErrNotSupported
	}
	conn, rw, err := internaltransport.HijackIfSupported(w.ResponseWriter)
	if err != nil {
		return nil, nil, err
	}
	w.started = true
	return conn, rw, nil
}

// checkLimitError runs only on the response-owning goroutine. Body reads may
// continue after an inner timeout returns, so they only publish immutable data.
func (w *bodyLimitResponseWriter) checkLimitError() {
	// Once a final error is committed, preserve its entire body even if a
	// concurrent reader discovers an overrun between WriteHeader and Write.
	if w.errorResponse {
		return
	}
	if failure := w.pending.Load(); failure != nil && failure.request.Context().Err() == nil {
		w.writeLimitError(failure.request, failure.maxBytes, failure.seenBytes, failure.at)
	}
}

func (w *bodyLimitResponseWriter) writeLimitError(r *http.Request, maxBytes, seenBytes int64, at time.Time) {
	if w.blocked {
		return
	}
	w.blocked = true
	if !w.started {
		w.started = true
		internaltransport.WriteTransportError(w.ResponseWriter, r, http.StatusRequestEntityTooLarge, contract.CodeRequestBodyTooLarge, "request body exceeds configured limit", map[string]any{
			"max_bytes":  maxBytes,
			"seen_bytes": seenBytes,
			"at":         at.UTC(),
		})
	}
}

type limitedBodyReader struct {
	w        *bodyLimitResponseWriter
	req      *http.Request
	r        io.ReadCloser
	maxBytes int64
	used     int64
	exceeded bool
	logger   log.StructuredLogger
	now      func() time.Time
}

func (l *limitedBodyReader) Read(p []byte) (int, error) {
	if l.exceeded {
		return 0, errRequestTooLarge
	}

	remaining := l.maxBytes - l.used
	if remaining <= 0 {
		var probe [1]byte
		n, err := l.r.Read(probe[:])
		if n > 0 {
			l.used += int64(n)
			return 0, l.failErr()
		}
		return 0, err
	}

	if int64(len(p)) > remaining {
		p = p[:int(remaining)+1]
	}

	n, err := l.r.Read(p)
	if int64(n) > remaining {
		l.used += int64(n)
		return int(remaining), l.failErr()
	}

	l.used += int64(n)
	return n, err
}

func (l *limitedBodyReader) Close() error {
	return l.r.Close()
}

func (l *limitedBodyReader) failErr() error {
	if !l.exceeded {
		l.exceeded = true
		l.w.pending.Store(&bodyLimitError{request: l.req, maxBytes: l.maxBytes, seenBytes: l.used, at: l.now()})
		if l.logger != nil {
			fields := internaltelemetry.MiddlewareLogFields(l.req, http.StatusRequestEntityTooLarge, 0)
			fields["max_bytes"] = l.maxBytes
			fields["seen_bytes"] = l.used
			internaltelemetry.RunSafeFinalizer(func() {
				l.logger.WithFields(log.Fields(internaltelemetry.RedactFields(fields))).Warn("request body too large")
			})
		}
	}

	return errRequestTooLarge
}
