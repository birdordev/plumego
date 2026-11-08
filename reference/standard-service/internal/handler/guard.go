package handler

import (
	"crypto/subtle"
	"net/http"

	"github.com/birdordev/plumego/contract"
	plumelog "github.com/birdordev/plumego/log"
)

// WriteKeyHeader is the request header that carries the write-operation key.
const WriteKeyHeader = "X-Write-Key"

const codeAuthKeyInvalid = "auth.key.invalid"

// RequireWriteKey returns a middleware that gates mutating operations behind a
// static key supplied via the X-Write-Key header.
//
// When key is empty the middleware is a no-op, allowing unauthenticated access
// during local development. Set APP_WRITE_KEY in production to enforce the guard.
//
// This demonstrates the per-route middleware wrapping pattern: callers wrap only
// the handlers that need protection rather than adding the check globally.
// Timing-safe comparison (crypto/subtle.ConstantTimeCompare) prevents attackers
// from using response time to guess the key byte-by-byte.
//
// Header choice: X-Write-Key is a private API key header rather than the standard
// Authorization: Bearer pattern from security/authn.StaticToken(). A private header
// suits service-to-service API keys where callers are automated tools or internal
// services — not browsers where credential stores manage Authorization headers.
// For user-facing bearer token authentication (login flows, OAuth), use
// security/authn.StaticToken() with security/authn.ExtractBearerToken() instead.
//
//	writeGuard := handler.RequireWriteKey(cfg.App.WriteKey, a.Core.Logger())
//	v1.post("/items", writeGuard(http.HandlerFunc(items.Create)))
func RequireWriteKey(key string, logger plumelog.StructuredLogger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		if key == "" {
			// Guard disabled — pass through without any header check.
			return next
		}
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			headerVal := r.Header.Get(WriteKeyHeader)
			if subtle.ConstantTimeCompare([]byte(headerVal), []byte(key)) != 1 {
				logWriteErr(logger, contract.WriteError(w, r, contract.NewErrorBuilder().
					Type(contract.TypeUnauthorized).
					Code(codeAuthKeyInvalid).
					Message("valid X-Write-Key header required").
					Build()))
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
