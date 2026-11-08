package circuitbreaker

import (
	"errors"
	"net/http"

	"github.com/birdordev/plumego/contract"
)

// Middleware creates a circuit breaker middleware
//
// Example:
//
//	app.Use(circuitbreaker.Middleware(circuitbreaker.Config{
//		Name:             "api",
//		FailureThreshold: 0.6,
//		Timeout:          30 * time.Second,
//	}))
func Middleware(config Config) func(http.Handler) http.Handler {
	cb := New(config)

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Try to execute request through circuit breaker
			err := cb.Call(func() error {
				// Wrap response writer to capture status code
				wrapper := &statusWriter{ResponseWriter: w, statusCode: http.StatusOK}

				// Execute handler
				next.ServeHTTP(wrapper, r)

				// Consider 5xx status codes as failures
				if wrapper.statusCode >= 500 {
					return ErrServerError
				}

				return nil
			})

			// If circuit breaker rejected the request
			if errors.Is(err, ErrCircuitOpen) {
				writeCircuitOpenResponse(w, r, cb)
				return
			}

			if errors.Is(err, ErrTooManyRequests) {
				writeTooManyRequestsResponse(w, r, cb)
				return
			}
		})
	}
}

// MiddlewareWithErrorHandler creates a circuit breaker middleware with custom error handling
func MiddlewareWithErrorHandler(config Config, errorHandler ErrorHandler) func(http.Handler) http.Handler {
	cb := New(config)

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			err := cb.Call(func() error {
				wrapper := &statusWriter{ResponseWriter: w, statusCode: http.StatusOK}
				next.ServeHTTP(wrapper, r)

				if wrapper.statusCode >= 500 {
					return ErrServerError
				}

				return nil
			})

			if err != nil {
				errorHandler(w, r, cb, err)
				return
			}
		})
	}
}

// ErrorHandler handles circuit breaker errors
type ErrorHandler func(w http.ResponseWriter, r *http.Request, cb *CircuitBreaker, err error)

const CodeCircuitOpen = "CIRCUIT_OPEN"

// statusWriter wraps http.ResponseWriter to capture status code
type statusWriter struct {
	http.ResponseWriter
	statusCode int
	written    bool
}

func (w *statusWriter) Unwrap() http.ResponseWriter {
	return w.ResponseWriter
}

func (w *statusWriter) WriteHeader(code int) {
	if !w.written {
		w.statusCode = code
		w.written = true
		w.ResponseWriter.WriteHeader(code)
	}
}

func (w *statusWriter) Flush() {
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (w *statusWriter) Write(b []byte) (int, error) {
	if !w.written {
		w.WriteHeader(http.StatusOK)
	}
	return safeWrite(w.ResponseWriter, b)
}

// writeCircuitOpenResponse writes a 503 response when circuit is open
func writeCircuitOpenResponse(w http.ResponseWriter, r *http.Request, cb *CircuitBreaker) {
	w.Header().Set("X-Circuit-Breaker-State", cb.State().String())
	ensureNoSniff(w.Header())
	_ = contract.WriteError(w, r, contract.NewErrorBuilder().
		Type(contract.TypeUnavailable).
		Code(CodeCircuitOpen).
		Message("circuit breaker is open; service temporarily unavailable").
		Detail("circuit", cb.Name()).
		Detail("state", cb.State().String()).
		Build())
}

// writeTooManyRequestsResponse writes a 429 response for rate limiting
func writeTooManyRequestsResponse(w http.ResponseWriter, r *http.Request, cb *CircuitBreaker) {
	w.Header().Set("X-Circuit-Breaker-State", cb.State().String())
	ensureNoSniff(w.Header())
	_ = contract.WriteError(w, r, contract.NewErrorBuilder().
		Type(contract.TypeRateLimited).
		Message("circuit breaker is half-open; too many concurrent requests").
		Detail("circuit", cb.Name()).
		Detail("state", cb.State().String()).
		Build())
}

// ErrServerError indicates a server error (5xx)
var ErrServerError = http.ErrAbortHandler
