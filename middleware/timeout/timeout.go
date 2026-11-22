package timeout

import (
	"context"
	"net/http"
	"strconv"
	"time"

	"github.com/birdordev/plumego/contract"
	internaltransport "github.com/birdordev/plumego/internal/httputil"
	mw "github.com/birdordev/plumego/middleware"
)

const (
	// Default timeout buffer limit: 10MB
	defaultTimeoutMaxBytes = 10 << 20
	// Responses larger than this cannot be replayed after timeout buffering and
	// are converted into a structured server error instead of being streamed.
	defaultMaxReplayBytes = 512 << 10 // 512KB
)

// Config customizes timeout middleware behavior.
//
// Timeout middleware enforces a maximum duration for request processing.
// If the downstream handler does not complete before the deadline, the request
// context is canceled and a 504 Gateway Timeout response is returned. Timeout
// cannot forcibly stop downstream side effects; handlers must observe the
// request context to stop work promptly.
//
// Example:
//
//	import "github.com/birdordev/plumego/middleware/timeout"
//
//	// Explicit configuration
//	config := timeout.Config{
//		Timeout:        10 * time.Second,
//		MaxBufferBytes: 5 << 20,      // 5MB max buffer
//		MaxReplayBytes: 1 << 20,      // 1MB replay threshold
//	}
//	handler := timeout.Middleware(config)(myHandler)
//
// The middleware buffers responses to allow timeout enforcement. Large
// responses that exceed MaxReplayBytes are not streamed through; they
// return a structured server error because the buffered response cannot be
// replayed safely.
//
// When a timeout occurs, it returns a 504 Gateway Timeout response with a
// structured error message. Cancellation observed before response commitment
// takes priority over a downstream result. Explicit parent cancellation returns
// without writing a response; an expired deadline returns 504. A request that
// is already canceled does not invoke the downstream handler. Panics accepted
// before cancellation are re-panicked on the request goroutine; configure
// OnPanic to observe panics discarded because of cancellation. Body writes
// beyond the committed Content-Length return http.ErrContentLength.
type Config struct {
	// Timeout is the maximum duration for request processing
	Timeout time.Duration

	// MaxBufferBytes is the maximum response size to buffer for timeout
	// enforcement. Responses larger than this are converted into a structured
	// server error because the buffered response cannot be replayed safely.
	MaxBufferBytes int

	// MaxReplayBytes is the largest response that can be buffered for timeout
	// replay. Larger responses return a structured server error because they
	// cannot be safely replayed after buffering is abandoned.
	MaxReplayBytes int

	// OnPanic observes downstream panics discarded because of cancellation,
	// after response handling has finished. Panics accepted before cancellation
	// re-panic to the caller so outer recovery middleware can handle them. The
	// callback is best-effort: it must not block, and callback panics are
	// recovered internally.
	OnPanic func(r *http.Request, recovered any)
}

// DefaultConfig returns default timeout buffering settings. Timeout is left
// unset so callers must opt into request deadlines explicitly.
func DefaultConfig() Config {
	return Config{
		MaxBufferBytes: defaultTimeoutMaxBytes,
		MaxReplayBytes: defaultMaxReplayBytes,
	}
}

// Middleware creates timeout middleware with explicit configuration.
//
// Example:
//
//	import "github.com/birdordev/plumego/middleware/timeout"
//
//	config := timeout.Config{
//		Timeout:        10 * time.Second,
//		MaxBufferBytes: 5 << 20,      // 5MB max buffer
//		MaxReplayBytes: 1 << 20,      // 1MB replay threshold
//	}
//	handler := timeout.Middleware(config)(myHandler)
func Middleware(cfg Config) mw.Middleware {
	if cfg.Timeout <= 0 {
		return func(next http.Handler) http.Handler {
			return next
		}
	}

	defaults := DefaultConfig()
	if cfg.MaxBufferBytes <= 0 {
		cfg.MaxBufferBytes = defaults.MaxBufferBytes
	}
	if cfg.MaxReplayBytes <= 0 {
		cfg.MaxReplayBytes = defaults.MaxReplayBytes
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx, cancel := context.WithTimeout(r.Context(), cfg.Timeout)
			defer cancel()

			r = r.WithContext(ctx)
			if ctx.Err() != nil {
				writeContextError(w, r, ctx)
				return
			}

			tw := newTimeoutResponseWriter(ctx, cfg)
			tw.routeDestination = w
			// Clone the outer headers so downstream edits remain private until replay.
			internaltransport.CopyHeaders(tw.Header(), w.Header())
			done := make(chan timeoutHandlerResult)
			var responseDone chan struct{}
			if cfg.OnPanic != nil {
				responseDone = make(chan struct{})
				defer close(responseDone)
			}

			go func() {
				result := timeoutHandlerResult{}
				defer func() {
					if rec := recover(); rec != nil {
						result.panicValue = rec
					}
					cfg.deliverTimeoutResult(ctx, done, responseDone, r, result)
				}()
				if ctx.Err() == nil {
					next.ServeHTTP(tw, r)
					result.returned = true
				}
			}()

			var result timeoutHandlerResult
			select {
			case result = <-done:
			case <-ctx.Done():
			}
			// This is the response commitment point. Observed cancellation wins
			// even when both select cases were ready. Only the receiver of a
			// handler result may replay its buffer or handle its panic.
			if ctx.Err() != nil {
				writeContextError(w, r, ctx)
				if result.panicValue != nil && cfg.OnPanic != nil {
					cfg.reportLatePanic(r, result.panicValue)
				}
				return
			}
			if result.panicValue != nil {
				panic(result.panicValue)
			}
			if !result.returned {
				writeTimeoutError(w, r, http.StatusInternalServerError, contract.CodeInternalError, "handler exited before completing the response")
				return
			}
			if tw.Overflowed() {
				writeTimeoutError(w, r, http.StatusInternalServerError, contract.CodeInternalError, "response exceeded buffer limit")
				return
			}
			tw.writeTo(w, r)
		})
	}
}

func writeContextError(w http.ResponseWriter, r *http.Request, ctx context.Context) {
	if ctx.Err() == context.DeadlineExceeded {
		writeTimeoutError(w, r, http.StatusGatewayTimeout, contract.CodeTimeout, "request timed out")
	}
}

func writeTimeoutError(w http.ResponseWriter, r *http.Request, status int, code, message string) {
	internaltransport.RemoveResponseFields(w.Header(), func(name string) bool {
		switch name {
		case "Content-Length", "Content-Encoding", "Content-Range", "Transfer-Encoding", "Etag", "Last-Modified":
			return true
		}
		return internaltransport.IsRepresentationDigestField(name)
	})
	internaltransport.WriteTransportError(w, r, status, code, message, nil)
}

func (cfg Config) deliverTimeoutResult(ctx context.Context, done chan<- timeoutHandlerResult, responseDone <-chan struct{}, r *http.Request, result timeoutHandlerResult) {
	select {
	case done <- result:
	case <-ctx.Done():
		if result.panicValue != nil && cfg.OnPanic != nil {
			// The request goroutine owns the response until it returns.
			<-responseDone
			cfg.reportLatePanic(r, result.panicValue)
		}
	}
}

func (cfg Config) reportLatePanic(r *http.Request, recovered any) {
	defer func() {
		_ = recover()
	}()
	cfg.OnPanic(r, recovered)
}

type timeoutHandlerResult struct {
	panicValue any
	returned   bool
}

func newTimeoutResponseWriter(ctx context.Context, cfg Config) *timeoutResponseWriter {
	return &timeoutResponseWriter{
		ctx:    ctx,
		cfg:    cfg,
		buffer: internaltransport.NewBufferedResponse(cfg.MaxBufferBytes),
	}
}

type timeoutResponseWriter struct {
	routeDestination    http.ResponseWriter
	ctx                 context.Context
	cfg                 Config
	buffer              *internaltransport.BufferedResponse
	writtenBytes        int64
	contentLength       int64
	lengthParsed        bool
	overflow            bool
	replayLimitExceeded bool // Whether the response exceeded the replay budget.
}

func (w *timeoutResponseWriter) Header() http.Header {
	return w.buffer.Header()
}

func (w *timeoutResponseWriter) WriteHeader(statusCode int) {
	w.buffer.WriteHeader(statusCode)
}

func (w *timeoutResponseWriter) Write(p []byte) (int, error) {
	select {
	case <-w.ctx.Done():
		return 0, w.ctx.Err()
	default:
	}

	if len(p) > 0 {
		switch w.buffer.StatusCode() {
		case http.StatusSwitchingProtocols, http.StatusNoContent, http.StatusNotModified:
			return 0, http.ErrBodyNotAllowed
		}
	}

	if w.overflow {
		return 0, http.ErrBodyNotAllowed
	}

	// The first write fixes the final headers. Use their length for every
	// subsequent write, including writes while replay has been abandoned.
	w.buffer.WriteHeader(http.StatusOK)
	w.writtenBytes += int64(len(p))
	if !w.lengthParsed {
		w.lengthParsed = true
		w.contentLength = -1
		if raw := w.buffer.FinalHeader().Get("Content-Length"); raw != "" {
			if length, err := strconv.ParseInt(raw, 10, 64); err == nil {
				w.contentLength = length
			}
		}
	}
	if w.contentLength >= 0 && w.writtenBytes > w.contentLength {
		return 0, http.ErrContentLength
	}

	// If buffering was abandoned, discard later writes and let WriteTo emit the
	// canonical large-response error.
	if w.replayLimitExceeded {
		return len(p), nil
	}

	currentSize := w.buffer.Len() + len(p)
	if currentSize > w.cfg.MaxReplayBytes {
		w.replayLimitExceeded = true
		w.buffer.ClearBody()
		return len(p), nil
	}

	n, err := w.buffer.Write(p)
	if err != nil {
		w.overflow = true
	}
	return n, err
}

func (w *timeoutResponseWriter) writeTo(dst http.ResponseWriter, r *http.Request) {
	if w.replayLimitExceeded {
		writeTimeoutError(dst, r, http.StatusInternalServerError, contract.CodeInternalError, "response too large for timeout buffering")
		return
	}

	// Normal buffered response
	_, _ = w.buffer.WriteTo(dst)
}

func (w *timeoutResponseWriter) Overflowed() bool {
	return w.overflow
}

// SetRouteContext forwards immutable metadata only, never response operations.
func (w *timeoutResponseWriter) SetRouteContext(ctx context.Context) {
	internaltransport.PublishRouteContext(w.routeDestination, ctx)
}

// SetRouteIdentity forwards metadata without touching the response.
func (w *timeoutResponseWriter) SetRouteIdentity(ctx context.Context, pattern, name string) {
	internaltransport.PublishRouteIdentity(w.routeDestination, ctx, pattern, name)
}
