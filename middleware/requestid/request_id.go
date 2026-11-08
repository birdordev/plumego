package requestid

import (
	"net/http"

	"github.com/birdordev/plumego/middleware"
)

type config struct {
	generate         func() string
	includeInRequest bool
}

// Option configures request ID middleware behavior.
type Option func(*config)

// WithGenerator sets the request ID generator used when a request does not
// already carry a request ID. Use this in production when request ID shape,
// source, or determinism must be controlled by the application.
func WithGenerator(fn func() string) Option {
	return func(cfg *config) {
		if fn != nil {
			cfg.generate = fn
		}
	}
}

// WithRequestHeader controls whether the generated request ID is written back
// to the inbound request header before calling the next handler.
func WithRequestHeader(enabled bool) Option {
	return func(cfg *config) {
		cfg.includeInRequest = enabled
	}
}

// Middleware stamps a canonical request ID into the request context and
// response header. Request IDs are correlation identifiers only; do not use
// them as secrets, tokens, nonces, or authorization material. When no generator
// is supplied, it uses NewRequestID and its package-local default generator
// state.
func Middleware(opts ...Option) middleware.Middleware {
	cfg := config{
		generate:         NewRequestID,
		includeInRequest: true,
	}
	for _, opt := range opts {
		if opt != nil {
			opt(&cfg)
		}
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			id := EnsureRequestID(r, cfg.generate)
			r = AttachRequestID(w, r, id, cfg.includeInRequest)
			next.ServeHTTP(w, r)
		})
	}
}
