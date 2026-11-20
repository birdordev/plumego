// Package versioning provides API version negotiation middleware
//
// This package implements multiple strategies for API versioning:
//   - Accept header (content negotiation): application/vnd.myapi.v2+json
//   - URL path: /v2/users/123
//   - Query parameter: /users/123?version=2
//   - Custom header: X-API-Version: 2
//
// Example usage:
//
//	import (
//		"github.com/birdordev/plumego/x/rest/versioning"
//		"github.com/birdordev/plumego/core"
//	)
//
//	app := core.New(core.DefaultConfig())
//
//	// Accept header strategy
//	app.Use(versioning.Middleware(versioning.Config{
//		Strategy:          versioning.StrategyAcceptHeader,
//		VendorPrefix:      "application/vnd.myapi",
//		DefaultVersion:    1,
//		SupportedVersions: []int{1, 2, 3},
//	}))
//
//	// URL path strategy
//	app.Use(versioning.Middleware(versioning.Config{
//		Strategy:          versioning.StrategyURLPath,
//		PathPrefix:        "/v",
//		DefaultVersion:    1,
//		SupportedVersions: []int{1, 2, 3},
//	}))
//
//	// Access version in handler
//	app.Get("/users/:id", func(w http.ResponseWriter, r *http.Request) {
//		version := versioning.VersionFromContext(r.Context())
//		// Handle request based on version
//	})
package versioning

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/birdordev/plumego/contract"
	mw "github.com/birdordev/plumego/middleware"
)

// CodeUnsupportedVersion is the canonical x/rest version-negotiation error code.
const CodeUnsupportedVersion = "unsupported_version"

// Strategy defines the version extraction strategy
type Strategy int

const (
	// StrategyAcceptHeader extracts version from Accept header
	// Example: Accept: application/vnd.myapi.v2+json
	StrategyAcceptHeader Strategy = iota

	// StrategyURLPath extracts version from URL path
	// Example: /v2/users/123
	StrategyURLPath

	// StrategyQueryParam extracts version from query parameter
	// Example: /users/123?version=2
	StrategyQueryParam

	// StrategyCustomHeader extracts version from custom header
	// Example: X-API-Version: 2
	StrategyCustomHeader
)

// String returns the strategy name
func (s Strategy) String() string {
	switch s {
	case StrategyAcceptHeader:
		return "accept-header"
	case StrategyURLPath:
		return "url-path"
	case StrategyQueryParam:
		return "query-param"
	case StrategyCustomHeader:
		return "custom-header"
	default:
		return "unknown"
	}
}

// Config holds version negotiation configuration
type Config struct {
	// Strategy is the version extraction strategy
	// Default: StrategyAcceptHeader
	Strategy Strategy

	// DefaultVersion is the version to use if none specified
	// Default: 1
	DefaultVersion int

	// SupportedVersions is copied when Middleware is constructed.
	// If empty, all versions are accepted
	// Default: nil (accept all)
	SupportedVersions []int

	// For StrategyAcceptHeader:
	// VendorPrefix is the vendor media type prefix
	// Example: "application/vnd.myapi" matches "application/vnd.myapi.v2+json"
	// Default: "application/vnd.api"
	VendorPrefix string

	// For StrategyURLPath:
	// PathPrefix is the version path prefix
	// Example: "/v" matches "/v2/users/123"
	// Default: "/v"
	PathPrefix string

	// For StrategyQueryParam:
	// ParamName is the query parameter name
	// Example: "version" matches "?version=2"
	// Default: "version"
	ParamName string

	// For StrategyCustomHeader:
	// HeaderName is the custom header name
	// Example: "X-API-Version"
	// Default: "X-API-Version"
	HeaderName string

	// StripVersionFromPath removes version from URL path (for StrategyURLPath)
	// Example: /v2/users/123 becomes /users/123
	// Default: true
	StripVersionFromPath bool

	// OnVersionMismatch is called when unsupported version is requested
	// If nil, returns 406 Not Acceptable
	OnVersionMismatch func(w http.ResponseWriter, r *http.Request, requested int)

	// OnVersionExtracted is called after version is extracted (optional)
	OnVersionExtracted func(r *http.Request, version int)
}

// versionKey is a collision-safe unexported struct context key
type versionKey struct{}

// applyDefaults returns a Config with default values applied
func applyDefaults(c Config) Config {
	if c.DefaultVersion == 0 {
		c.DefaultVersion = 1
	}
	if c.VendorPrefix == "" {
		c.VendorPrefix = "application/vnd.api"
	}
	if c.PathPrefix == "" {
		c.PathPrefix = "/v"
	}
	if c.ParamName == "" {
		c.ParamName = "version"
	}
	if c.HeaderName == "" {
		c.HeaderName = "X-API-Version"
	}
	if !c.StripVersionFromPath {
		c.StripVersionFromPath = true
	}
	return c
}

// Middleware creates a version negotiation middleware
func Middleware(config Config) mw.Middleware {
	cfg := applyDefaults(config)
	cfg.SupportedVersions = slices.Clone(cfg.SupportedVersions)

	// Pre-compile Accept header regex at construction time
	var acceptRe *regexp.Regexp
	if cfg.Strategy == StrategyAcceptHeader {
		pattern := regexp.QuoteMeta(cfg.VendorPrefix) + `\.v(\d+)(?:\+|;|[ \t]*(?:,|$))`
		acceptRe = regexp.MustCompile(pattern)
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			var version int
			var segment pathVersion
			if cfg.Strategy == StrategyURLPath {
				segment = parsePathVersion(r.URL, cfg.PathPrefix)
				version = segment.number
			} else {
				version = extractVersion(r, cfg, acceptRe)
			}
			if version <= 0 {
				version = cfg.DefaultVersion
			}

			if len(cfg.SupportedVersions) > 0 && !isVersionSupported(version, cfg.SupportedVersions) {
				if cfg.OnVersionMismatch != nil {
					cfg.OnVersionMismatch(w, r, version)
				} else {
					_ = contract.WriteError(w, r, contract.NewErrorBuilder().
						Type(contract.TypeNotAcceptable).
						Message(fmt.Sprintf("unsupported API version: %d", version)).
						Build())
				}
				return
			}

			if segment.number > 0 && cfg.StripVersionFromPath {
				forwarded := *r
				forwarded.URL = segment.strip(r.URL)
				r = &forwarded
			}

			if cfg.OnVersionExtracted != nil {
				cfg.OnVersionExtracted(r, version)
			}

			ctx := WithVersion(r.Context(), version)
			w.Header().Set("X-API-Version", strconv.Itoa(version))
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// WithVersion stores the API version in context.
func WithVersion(ctx context.Context, version int) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, versionKey{}, version)
}

// VersionFromContext returns the API version from context.
// Returns 0 if version not found in context.
func VersionFromContext(ctx context.Context) int {
	if ctx == nil {
		return 0
	}
	if v, ok := ctx.Value(versionKey{}).(int); ok {
		return v
	}
	return 0
}

// extractVersion handles version sources that do not rewrite the request path.
func extractVersion(r *http.Request, config Config, acceptRe *regexp.Regexp) int {
	switch config.Strategy {
	case StrategyAcceptHeader:
		return extractFromAccept(r, acceptRe)
	case StrategyQueryParam:
		return extractFromQuery(r, config.ParamName)
	case StrategyCustomHeader:
		return extractFromHeader(r, config.HeaderName)
	default:
		return 0
	}
}

// extractFromAccept extracts version from Accept header using a pre-compiled regex
func extractFromAccept(r *http.Request, re *regexp.Regexp) int {
	accept := strings.Join(r.Header.Values("Accept"), ",")
	if accept == "" || re == nil {
		return 0
	}

	matches := re.FindStringSubmatch(accept)
	if len(matches) >= 2 {
		if v, err := strconv.Atoi(matches[1]); err == nil {
			return v
		}
	}

	return 0
}

type pathVersion struct {
	number     int
	pathEnd    int
	escapedEnd int
}

func parsePathVersion(u *url.URL, prefix string) pathVersion {
	path, ok := strings.CutPrefix(u.Path, prefix)
	if !ok {
		return pathVersion{}
	}
	digits, _, _ := strings.Cut(path, "/")
	for i := range len(digits) {
		if digits[i] < '0' || digits[i] > '9' {
			return pathVersion{}
		}
	}
	number, err := strconv.Atoi(digits)
	if err != nil || number <= 0 {
		return pathVersion{}
	}

	// Keep the original spelling for stripping (including leading zeros).
	// A percent-encoded slash does not delimit a URL path segment.
	pathEnd := len(prefix) + len(digits)
	escaped := u.EscapedPath()
	escapedEnd := 0
	for range pathEnd {
		if escaped[escapedEnd] == '%' {
			escapedEnd += 3
		} else {
			escapedEnd++
		}
	}
	if escapedEnd < len(escaped) && escaped[escapedEnd] != '/' {
		return pathVersion{}
	}
	return pathVersion{number: number, pathEnd: pathEnd, escapedEnd: escapedEnd}
}

func (v pathVersion) strip(u *url.URL) *url.URL {
	result := *u
	result.Path = u.Path[v.pathEnd:]
	if result.Path == "" {
		result.Path = "/"
		result.RawPath = ""
	} else if u.RawPath != "" {
		result.RawPath = u.EscapedPath()[v.escapedEnd:]
	}
	return &result
}

// extractFromQuery extracts version from query parameter
// Example: ?version=2 -> 2
func extractFromQuery(r *http.Request, paramName string) int {
	versionStr := r.URL.Query().Get(paramName)
	if versionStr == "" {
		return 0
	}

	if v, err := strconv.Atoi(versionStr); err == nil {
		return v
	}

	return 0
}

// extractFromHeader extracts version from custom header
// Example: X-API-Version: 2 -> 2
func extractFromHeader(r *http.Request, headerName string) int {
	versionStr := r.Header.Get(headerName)
	if versionStr == "" {
		return 0
	}

	if v, err := strconv.Atoi(versionStr); err == nil {
		return v
	}

	return 0
}

// isVersionSupported checks if version is in supported list
func isVersionSupported(version int, supported []int) bool {
	for _, v := range supported {
		if version == v {
			return true
		}
	}
	return false
}

// Extractor is a custom version extractor function
type Extractor func(r *http.Request) int

// CustomExtractor creates a middleware with custom version extraction.
// It copies supportedVersions at construction.
func CustomExtractor(extractor Extractor, defaultVersion int, supportedVersions []int) mw.Middleware {
	supportedVersions = slices.Clone(supportedVersions)
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			version := extractor(r)

			if version <= 0 {
				version = defaultVersion
			}

			if len(supportedVersions) > 0 && !isVersionSupported(version, supportedVersions) {
				_ = contract.WriteError(w, r, contract.NewErrorBuilder().
					Type(contract.TypeNotAcceptable).
					Message(fmt.Sprintf("unsupported API version: %d", version)).
					Build())
				return
			}

			ctx := WithVersion(r.Context(), version)
			w.Header().Set("X-API-Version", strconv.Itoa(version))

			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}
