package cors

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	internaltransport "github.com/birdordev/plumego/internal/httputil"
	"github.com/birdordev/plumego/middleware"
)

// ErrStrictDefaultOriginsRequired is returned when strict CORS defaults are
// requested without at least one non-blank allowed origin.
var ErrStrictDefaultOriginsRequired = errors.New("cors: strict defaults require at least one allowed origin")

// ErrStrictDefaultWildcardOrigin is returned when strict CORS defaults are
// requested with a wildcard origin.
var ErrStrictDefaultWildcardOrigin = errors.New("cors: strict defaults do not allow wildcard origins")

// CORSOptions configures Cross-Origin Resource Sharing (CORS) behavior.
//
// Security notice: when AllowedOrigins is nil or empty, [Middleware] defaults to
// the wildcard origin ["*"], which allows cross-origin requests from any domain.
// This is appropriate for fully public APIs but is an open CORS policy and should
// not be used when AllowCredentials is true or when the API is not public.
// For production deployments use [StrictDefaultOptions] instead, which requires
// at least one explicit origin and rejects wildcards.
type CORSOptions struct {
	AllowedOrigins   []string
	AllowedMethods   []string
	AllowedHeaders   []string
	AllowCredentials bool
	ExposeHeaders    []string
	MaxAge           time.Duration
}

// StrictDefaultOptions returns production-oriented CORS defaults and reports
// invalid strict origin configuration without panicking.
func StrictDefaultOptions(allowedOrigins ...string) (CORSOptions, error) {
	origins, err := normalizeStrictOrigins(allowedOrigins)
	if err != nil {
		return CORSOptions{}, err
	}
	if len(origins) == 0 {
		return CORSOptions{}, ErrStrictDefaultOriginsRequired
	}
	return CORSOptions{
		AllowedOrigins: origins,
		AllowedMethods: defaultAllowedMethods(),
		AllowedHeaders: defaultAllowedHeaders(),
	}, nil
}

func (o CORSOptions) withDefaults() CORSOptions {
	o.AllowedOrigins = normalizeList(o.AllowedOrigins)
	if len(o.AllowedOrigins) == 0 {
		o.AllowedOrigins = []string{"*"}
	}
	o.AllowedMethods = normalizeList(o.AllowedMethods)
	if len(o.AllowedMethods) == 0 {
		o.AllowedMethods = defaultAllowedMethods()
	}
	o.AllowedHeaders = normalizeList(o.AllowedHeaders)
	if len(o.AllowedHeaders) == 0 {
		o.AllowedHeaders = defaultAllowedHeaders()
	}
	o.ExposeHeaders = normalizeList(o.ExposeHeaders)
	return o
}

func normalizeStrictOrigins(origins []string) ([]string, error) {
	normalized := make([]string, 0, len(origins))
	for _, origin := range origins {
		origin = strings.TrimSpace(origin)
		if origin == "" {
			continue
		}
		if origin == "*" {
			return nil, ErrStrictDefaultWildcardOrigin
		}
		normalized = append(normalized, origin)
	}
	return normalized, nil
}

func normalizeList(values []string) []string {
	normalized := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		normalized = append(normalized, value)
	}
	return normalized
}

func defaultAllowedMethods() []string {
	return []string{"GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"}
}

func defaultAllowedHeaders() []string {
	return []string{"Accept", "Accept-Language", "Content-Language", "Content-Type", "Authorization"}
}

func contains(slice []string, s string) bool {
	for _, v := range slice {
		if v == s {
			return true
		}
	}
	return false
}

func joinOrDefault(slice []string, def string) string {
	if len(slice) == 0 {
		return def
	}
	return strings.Join(slice, ", ")
}

func containsFold(slice []string, s string) bool {
	for _, v := range slice {
		if strings.EqualFold(v, s) {
			return true
		}
	}
	return false
}

// Middleware provides configurable CORS support.
func Middleware(opts CORSOptions) middleware.Middleware {
	opts = opts.withDefaults()
	allowAllOrigins := contains(opts.AllowedOrigins, "*")

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			origin := r.Header.Get("Origin")
			if origin == "" {
				next.ServeHTTP(w, r)
				return
			}

			var allowOriginValue string
			if allowAllOrigins {
				if opts.AllowCredentials {
					allowOriginValue = origin
				} else {
					allowOriginValue = "*"
				}
			} else if contains(opts.AllowedOrigins, origin) {
				allowOriginValue = origin
			} else {
				next.ServeHTTP(w, r)
				return
			}

			if r.Method == http.MethodOptions && r.Header.Get("Access-Control-Request-Method") != "" {
				requestMethod := strings.TrimSpace(r.Header.Get("Access-Control-Request-Method"))
				if !containsFold(opts.AllowedMethods, requestMethod) {
					next.ServeHTTP(w, r)
					return
				}

				var allowHeadersValue string
				reqHeaders := r.Header.Get("Access-Control-Request-Headers")
				if reqHeaders != "" {
					if contains(opts.AllowedHeaders, "*") {
						allowed, ok := normalizeRequestedHeaders(reqHeaders)
						if !ok {
							next.ServeHTTP(w, r)
							return
						}
						allowHeadersValue = strings.Join(allowed, ", ")
					} else {
						allowed, ok := allowedRequestedHeaders(reqHeaders, opts.AllowedHeaders)
						if !ok {
							next.ServeHTTP(w, r)
							return
						}
						allowHeadersValue = strings.Join(allowed, ", ")
					}
				} else {
					allowHeadersValue = joinOrDefault(opts.AllowedHeaders, "")
				}

				internaltransport.AddVary(w.Header(), "Origin", "Access-Control-Request-Method", "Access-Control-Request-Headers")
				w.Header().Set("Access-Control-Allow-Origin", allowOriginValue)
				if opts.AllowCredentials {
					w.Header().Set("Access-Control-Allow-Credentials", "true")
				}
				w.Header().Set("Access-Control-Allow-Methods", joinOrDefault(opts.AllowedMethods, "GET, POST, OPTIONS"))
				if allowHeadersValue != "" {
					w.Header().Set("Access-Control-Allow-Headers", allowHeadersValue)
				}

				if opts.MaxAge > 0 {
					w.Header().Set("Access-Control-Max-Age", strconv.FormatInt(int64(opts.MaxAge/time.Second), 10))
				}

				w.WriteHeader(http.StatusNoContent)
				return
			}

			internaltransport.AddVary(w.Header(), "Origin")
			w.Header().Set("Access-Control-Allow-Origin", allowOriginValue)
			if opts.AllowCredentials {
				w.Header().Set("Access-Control-Allow-Credentials", "true")
			}
			if len(opts.ExposeHeaders) > 0 {
				w.Header().Set("Access-Control-Expose-Headers", joinOrDefault(opts.ExposeHeaders, ""))
			}

			next.ServeHTTP(w, r)
		})
	}
}

func allowedRequestedHeaders(requestHeaders string, allowedHeaders []string) ([]string, bool) {
	allowed, ok := normalizeRequestedHeaders(requestHeaders)
	if !ok {
		return nil, false
	}
	for _, h := range allowed {
		if !containsFold(allowedHeaders, h) {
			return nil, false
		}
	}
	return allowed, true
}

func normalizeRequestedHeaders(requestHeaders string) ([]string, bool) {
	reqs := strings.Split(requestHeaders, ",")
	allowed := make([]string, 0, len(reqs))
	for _, h := range reqs {
		h = strings.TrimSpace(h)
		if h == "" {
			continue
		}
		allowed = append(allowed, h)
	}
	if len(allowed) == 0 {
		return nil, false
	}
	return allowed, true
}
