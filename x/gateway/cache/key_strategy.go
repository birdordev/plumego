package cache

import (
	"fmt"
	"hash/fnv"
	"net/http"
	"slices"
	"sort"
	"strings"
)

// URLOnlyKeyStrategy generates cache keys based on URL only
// Useful for caching identical resources regardless of client capabilities
type URLOnlyKeyStrategy struct{}

// NewURLOnlyKeyStrategy creates a URL-only key strategy
func NewURLOnlyKeyStrategy() *URLOnlyKeyStrategy {
	return &URLOnlyKeyStrategy{}
}

// Generate generates a cache key from URL only
func (s *URLOnlyKeyStrategy) Generate(r *http.Request) string {
	h := fnv.New64a()
	h.Write([]byte(r.Method))
	h.Write([]byte("|"))
	h.Write([]byte(r.URL.String()))
	return fmt.Sprintf("%x", h.Sum64())
}

// HeaderAwareKeyStrategy generates cache keys including specific headers
// Useful for content negotiation and client-specific responses
type HeaderAwareKeyStrategy struct {
	Headers []string
}

// NewHeaderAwareKeyStrategy creates a header-aware key strategy
// The strategy owns a copy of the input slice.
func NewHeaderAwareKeyStrategy(headers []string) *HeaderAwareKeyStrategy {
	return &HeaderAwareKeyStrategy{
		Headers: slices.Clone(headers),
	}
}

// Generate generates a cache key including specified headers
func (s *HeaderAwareKeyStrategy) Generate(r *http.Request) string {
	h := fnv.New64a()

	// Method
	h.Write([]byte(r.Method))
	h.Write([]byte("|"))

	// URL
	h.Write([]byte(r.URL.String()))
	h.Write([]byte("|"))

	// Headers (sorted for consistency)
	headers := make([]string, len(s.Headers))
	copy(headers, s.Headers)
	sort.Strings(headers)

	for _, header := range headers {
		value := r.Header.Get(header)
		if value != "" {
			h.Write([]byte(header))
			h.Write([]byte(":"))
			h.Write([]byte(value))
			h.Write([]byte("|"))
		}
	}

	return fmt.Sprintf("%x", h.Sum64())
}

// QueryParamKeyStrategy generates cache keys including query parameters
// Useful for parameterized API endpoints
type QueryParamKeyStrategy struct {
	// IgnoreParams is a list of query parameters to ignore (e.g., tracking params)
	IgnoreParams []string
}

// NewQueryParamKeyStrategy creates a query parameter key strategy
// The strategy owns a copy of the input slice.
func NewQueryParamKeyStrategy(ignoreParams []string) *QueryParamKeyStrategy {
	return &QueryParamKeyStrategy{
		IgnoreParams: slices.Clone(ignoreParams),
	}
}

// Generate generates a cache key including query parameters
func (s *QueryParamKeyStrategy) Generate(r *http.Request) string {
	h := fnv.New64a()

	// Method
	h.Write([]byte(r.Method))
	h.Write([]byte("|"))

	// Path (without query)
	h.Write([]byte(r.URL.Path))
	h.Write([]byte("|"))

	// Query parameters (sorted, excluding ignored)
	query := r.URL.Query()
	keys := make([]string, 0, len(query))

	for key := range query {
		if !s.shouldIgnoreParam(key) {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)

	for _, key := range keys {
		values := query[key]
		sort.Strings(values)
		for _, value := range values {
			h.Write([]byte(key))
			h.Write([]byte("="))
			h.Write([]byte(value))
			h.Write([]byte("&"))
		}
	}

	return fmt.Sprintf("%x", h.Sum64())
}

func (s *QueryParamKeyStrategy) shouldIgnoreParam(param string) bool {
	for _, ignored := range s.IgnoreParams {
		if strings.EqualFold(param, ignored) {
			return true
		}
	}
	return false
}

// UserAwareKeyStrategy generates cache keys per user
// Useful for user-specific content that should not be shared
type UserAwareKeyStrategy struct {
	// UserIDHeader is the header containing user ID (e.g., "X-User-ID")
	UserIDHeader string

	// FallbackStrategy is used if no user ID found
	FallbackStrategy KeyStrategy
}

// NewUserAwareKeyStrategy creates a user-aware key strategy
func NewUserAwareKeyStrategy(userIDHeader string) *UserAwareKeyStrategy {
	return &UserAwareKeyStrategy{
		UserIDHeader:     userIDHeader,
		FallbackStrategy: NewDefaultKeyStrategy(),
	}
}

// Generate generates a cache key including user ID
func (s *UserAwareKeyStrategy) Generate(r *http.Request) string {
	userID := r.Header.Get(s.UserIDHeader)
	if userID == "" && s.FallbackStrategy != nil {
		return s.FallbackStrategy.Generate(r)
	}

	h := fnv.New64a()

	// User ID
	h.Write([]byte(userID))
	h.Write([]byte("|"))

	// Method
	h.Write([]byte(r.Method))
	h.Write([]byte("|"))

	// URL
	h.Write([]byte(r.URL.String()))

	return fmt.Sprintf("%x", h.Sum64())
}

// TenantAwareKeyStrategy generates cache keys per tenant
// Useful for multi-tenant applications
type TenantAwareKeyStrategy struct {
	// TenantIDHeader is the header containing tenant ID (e.g., "X-Tenant-ID")
	TenantIDHeader string

	// FallbackStrategy is used if no tenant ID found
	FallbackStrategy KeyStrategy
}

// NewTenantAwareKeyStrategy creates a tenant-aware key strategy
func NewTenantAwareKeyStrategy(tenantIDHeader string) *TenantAwareKeyStrategy {
	return &TenantAwareKeyStrategy{
		TenantIDHeader:   tenantIDHeader,
		FallbackStrategy: NewDefaultKeyStrategy(),
	}
}

// Generate generates a cache key including tenant ID
func (s *TenantAwareKeyStrategy) Generate(r *http.Request) string {
	tenantID := r.Header.Get(s.TenantIDHeader)
	if tenantID == "" && s.FallbackStrategy != nil {
		return s.FallbackStrategy.Generate(r)
	}

	h := fnv.New64a()

	// Tenant ID
	h.Write([]byte(tenantID))
	h.Write([]byte("|"))

	// Method
	h.Write([]byte(r.Method))
	h.Write([]byte("|"))

	// URL
	h.Write([]byte(r.URL.String()))

	return fmt.Sprintf("%x", h.Sum64())
}

// CompositeKeyStrategy combines multiple strategies
// Useful for complex caching scenarios
type CompositeKeyStrategy struct {
	Strategies []KeyStrategy
}

// NewCompositeKeyStrategy creates a composite key strategy
// The strategy owns a copy of the input slice.
func NewCompositeKeyStrategy(strategies ...KeyStrategy) *CompositeKeyStrategy {
	return &CompositeKeyStrategy{
		Strategies: slices.Clone(strategies),
	}
}

// Generate generates a cache key by combining multiple strategies
func (s *CompositeKeyStrategy) Generate(r *http.Request) string {
	h := fnv.New64a()

	for i, strategy := range s.Strategies {
		key := strategy.Generate(r)
		h.Write([]byte(key))
		if i < len(s.Strategies)-1 {
			h.Write([]byte("|"))
		}
	}

	return fmt.Sprintf("%x", h.Sum64())
}

// PathPatternKeyStrategy generates cache keys based on path patterns
// Useful for caching similar resources together
type PathPatternKeyStrategy struct {
	// Patterns maps path prefixes to custom key generators
	Patterns map[string]KeyStrategy

	// DefaultStrategy is used if no pattern matches
	DefaultStrategy KeyStrategy
}

// NewPathPatternKeyStrategy creates a path pattern key strategy
func NewPathPatternKeyStrategy(patterns map[string]KeyStrategy) *PathPatternKeyStrategy {
	return &PathPatternKeyStrategy{
		Patterns:        patterns,
		DefaultStrategy: NewDefaultKeyStrategy(),
	}
}

// Generate generates a cache key based on path pattern
func (s *PathPatternKeyStrategy) Generate(r *http.Request) string {
	path := r.URL.Path

	// Find matching pattern
	for prefix, strategy := range s.Patterns {
		if strings.HasPrefix(path, prefix) {
			return strategy.Generate(r)
		}
	}

	// Use default strategy
	if s.DefaultStrategy != nil {
		return s.DefaultStrategy.Generate(r)
	}

	return NewDefaultKeyStrategy().Generate(r)
}

// NoCacheKeyStrategy always returns empty key (effectively disables caching)
// Useful for conditional caching scenarios
type NoCacheKeyStrategy struct{}

// NewNoCacheKeyStrategy creates a no-cache key strategy
func NewNoCacheKeyStrategy() *NoCacheKeyStrategy {
	return &NoCacheKeyStrategy{}
}

// Generate always returns empty string (no caching)
func (s *NoCacheKeyStrategy) Generate(r *http.Request) string {
	return ""
}
