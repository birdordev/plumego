// Package securityheaders provides the HTTP security-header middleware. It adapts the
// policy types in [security/headers] into the [middleware.Middleware] signature
// so callers can wire a security-header policy into the standard middleware chain.
package securityheaders

import (
	"net/http"

	"github.com/birdordev/plumego/middleware"
	"github.com/birdordev/plumego/security/headers"
)

// Config controls security-header middleware behavior.
type Config struct {
	Policy *headers.Policy
}

// DefaultConfig returns a Config that applies the default security header policy.
// Pass DefaultConfig() (or an empty Config{}) to Middleware to use the default policy.
func DefaultConfig() Config {
	return Config{}
}

// Middleware applies a security header policy to responses.
//
// This middleware adds security-related HTTP headers to responses to protect
// against common web vulnerabilities such as XSS, clickjacking, and MIME sniffing.
//
// Example:
//
//	import "github.com/birdordev/plumego/middleware/securityheaders"
//	import "github.com/birdordev/plumego/security/headers"
//
//	// Use default security policy
//	mw, err := securityheaders.Middleware(securityheaders.Config{})
//
//	// Or with custom policy
//	policy := &headers.Policy{
//		FrameOptions: "DENY",
//		ContentTypeOptions: "nosniff",
//		ContentSecurityPolicy: headers.StrictCSP(),
//	}
//	mw, err = securityheaders.Middleware(securityheaders.Config{Policy: policy})
//	_ = err
//	handler := mw(myHandler)
//
// The default policy includes:
//   - X-Frame-Options: SAMEORIGIN (prevents clickjacking)
//   - X-Content-Type-Options: nosniff (prevents MIME sniffing)
//   - Referrer-Policy: strict-origin-when-cross-origin
//
// For stronger XSS protection, set Content-Security-Policy explicitly,
// e.g. use headers.StrictCSP() or headers.StrictPolicy().
//
// Invalid custom policies fail during construction.
//
// Note: This middleware should be applied early in the middleware chain to
// ensure security headers are set for all responses.
func Middleware(config Config) (middleware.Middleware, error) {
	effective := headers.DefaultPolicy()
	if config.Policy != nil {
		effective = *config.Policy
		if config.Policy.Additional != nil {
			effective.Additional = make(map[string]string, len(config.Policy.Additional))
			for key, value := range config.Policy.Additional {
				effective.Additional[key] = value
			}
		}
		if err := effective.Validate(); err != nil {
			return nil, err
		}
	}

	effective = effective.Compile()

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			effective.Apply(w, r)
			next.ServeHTTP(w, r)
		})
	}, nil
}
