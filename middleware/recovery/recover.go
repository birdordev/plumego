package recovery

import (
	"errors"
	"net/http"
	"reflect"

	contract "github.com/spcent/plumego/contract"
	internaltransport "github.com/spcent/plumego/internal/httputil"
	"github.com/spcent/plumego/log"
	"github.com/spcent/plumego/middleware"
	internaltelemetry "github.com/spcent/plumego/middleware/internal/telemetry"
)

// ErrNilLogger is returned when recovery is configured without a logger.
var ErrNilLogger = errors.New("recovery: logger cannot be nil")

// Config controls recovery middleware behavior.
type Config struct {
	Logger log.StructuredLogger
}

// Middleware recovers from panics in request handlers and returns a 500 Internal Server Error.
//
// This middleware prevents the entire application from crashing when a panic occurs in a request handler.
// It logs sanitized panic metadata server-side and returns a generic 500 response to the client.
// Panic details are intentionally omitted from the response to avoid leaking internal state.
// http.ErrAbortHandler is re-thrown so net/http can abort the response.
//
// Example:
//
//	import "github.com/spcent/plumego/middleware/recovery"
//
//	mw, err := recovery.Middleware(recovery.Config{Logger: logger})
//	if err != nil {
//		return err
//	}
//	handler := mw(myHandler)
//
// When a panic occurs, the middleware:
//  1. Recovers the panic and prevents the application from crashing
//  2. Logs sanitized panic metadata with trace ID
//  3. Returns a generic 500 Internal Server Error response (no internal details exposed)
//
// Note: This middleware should be placed early in the middleware chain to ensure
// it can catch panics from all downstream handlers.
func Middleware(config Config) (middleware.Middleware, error) {
	if config.Logger == nil {
		return nil, ErrNilLogger
	}
	return func(next http.Handler) http.Handler {
		return recoveryHandler(next, config.Logger)
	}, nil
}

func recoveryHandler(next http.Handler, logger log.StructuredLogger) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rw := &recoveryResponseWriter{BaseWrappedResponseWriter: internaltransport.BaseWrappedResponseWriter{ResponseWriter: w}}
		defer func() {
			if rec := recover(); rec != nil {
				if rec == http.ErrAbortHandler {
					panic(rec)
				}
				fields := internaltelemetry.MiddlewareLogFields(r, http.StatusInternalServerError, 0)
				fields["panic_type"] = panicType(rec)
				internaltelemetry.RunSafeFinalizer(func() {
					logger.WithFields(log.Fields(internaltelemetry.RedactFields(fields))).Error("panic recovered")
				})
				if rw.Written() {
					return
				}
				internaltransport.WriteTransportError(rw, r, http.StatusInternalServerError, contract.CodeInternalError, "internal server error", nil)
			}
		}()
		next.ServeHTTP(rw, r)
	})
}

type recoveryResponseWriter struct {
	internaltransport.BaseWrappedResponseWriter
}

func panicType(rec any) string {
	if rec == nil {
		return "unknown"
	}
	return reflect.TypeOf(rec).String()
}
