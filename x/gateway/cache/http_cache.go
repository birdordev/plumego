// Package cache provides HTTP response caching middleware
//
// This package contains production-grade HTTP caching logic with features including:
//   - Multiple cache key strategies (URL, headers, custom)
//   - Pluggable storage backends (in-memory LRU, user-provided)
//   - Cache-Control header support (max-age, no-cache, no-store)
//   - Conditional requests (ETag, Last-Modified, If-None-Match)
//   - Configurable TTL and size limits
//   - Thread-safe concurrent access
//
// Example usage:
//
//	import (
//		"time"
//
//		"github.com/spcent/plumego/core"
//		"github.com/spcent/plumego/x/gateway/cache"
//	)
//
//	cfg := core.DefaultConfig()
//	app := core.New(cfg, core.AppDependencies{})
//
//	// Simple caching with defaults
//	if err := app.Use(cache.Middleware(cache.Config{
//		DefaultTTL: 5 * time.Minute,
//	})); err != nil {
//		panic(err)
//	}
//
//	// Advanced configuration
//	if err := app.Use(cache.Middleware(cache.Config{
//		Store:                   cache.NewMemoryStore(1000),
//		KeyStrategy:             cache.NewDefaultKeyStrategy(),
//		DefaultTTL:              5 * time.Minute,
//		MaxSize:                 1 << 20, // 1MB
//		Methods:                 []string{"GET", "HEAD"},
//		StatusCodes:             []int{200, 301, 404},
//		RespectCacheControl:     true,
//		EnableConditionalRequests: true,
//	})); err != nil {
//		panic(err)
//	}
package cache

import (
	"fmt"
	"hash/fnv"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/spcent/plumego/internal/httputil"
)

// Store is the interface for cache storage backends
type Store interface {
	// Get retrieves a cached response
	Get(key string) (*CachedResponse, bool)

	// Set stores a response in cache with TTL
	Set(key string, resp *CachedResponse, ttl time.Duration)

	// Delete removes a cached response
	Delete(key string)

	// Clear removes all cached responses
	Clear()

	// Stats returns cache statistics
	Stats() StoreStats
}

// StoreStats holds cache store statistics
type StoreStats struct {
	Items     int
	Hits      uint64
	Misses    uint64
	Evictions uint64
	Size      int64 // Approximate size in bytes
}

// KeyStrategy generates cache keys from requests
type KeyStrategy interface {
	Generate(r *http.Request) string
}

// CachedResponse represents a cached HTTP response
type CachedResponse struct {
	// HTTP response data
	StatusCode int
	Header     http.Header
	Body       []byte

	// Cache metadata
	CachedAt  time.Time
	ExpiresAt time.Time
	ETag      string

	// Size tracking
	Size int64
}

// Config holds cache middleware configuration
type Config struct {
	// Store is the cache storage backend
	// Default: NewMemoryStore(1000)
	Store Store

	// KeyStrategy generates cache keys
	// Default: NewDefaultKeyStrategy()
	KeyStrategy KeyStrategy

	// DefaultTTL is the default time-to-live for cached responses
	// Default: 5 minutes
	DefaultTTL time.Duration

	// MaxSize is the maximum cacheable response size in bytes
	// Responses larger than this will not be cached
	// Default: 1MB
	MaxSize int64

	// Methods is the list of HTTP methods to cache
	// Default: ["GET", "HEAD"]
	Methods []string

	// StatusCodes is the list of status codes to cache
	// Default: [200, 301, 404]
	StatusCodes []int

	// RespectCacheControl respects Cache-Control headers from backend
	// Default: true
	RespectCacheControl bool

	// EnableConditionalRequests enables ETag and If-None-Match support
	// Default: true
	EnableConditionalRequests bool

	// OnCacheHit is called when a cache hit occurs (optional)
	OnCacheHit func(key string, resp *CachedResponse)

	// OnCacheMiss is called when a cache miss occurs (optional)
	OnCacheMiss func(key string)
}

// WithDefaults returns a Config with default values applied
func (c *Config) WithDefaults() *Config {
	config := *c

	if config.Store == nil {
		config.Store = NewMemoryStore(1000)
	}

	if config.KeyStrategy == nil {
		config.KeyStrategy = NewDefaultKeyStrategy()
	}

	if config.DefaultTTL == 0 {
		config.DefaultTTL = 5 * time.Minute
	}

	if config.MaxSize == 0 {
		config.MaxSize = 1 << 20 // 1MB
	}

	if len(config.Methods) == 0 {
		config.Methods = []string{"GET", "HEAD"}
	}

	if len(config.StatusCodes) == 0 {
		config.StatusCodes = []int{200, 301, 404}
	}

	if !config.RespectCacheControl {
		config.RespectCacheControl = true
	}

	if !config.EnableConditionalRequests {
		config.EnableConditionalRequests = true
	}

	return &config
}

// Middleware creates a caching middleware
func Middleware(config Config) func(http.Handler) http.Handler {
	cfg := config.WithDefaults()

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Only cache configured methods
			if !isCacheableMethod(r.Method, cfg.Methods) {
				next.ServeHTTP(w, r)
				return
			}

			// Check for no-cache directive in request
			if cfg.RespectCacheControl && hasNoCacheDirective(r) {
				next.ServeHTTP(w, r)
				return
			}

			// Generate cache key
			key := cfg.KeyStrategy.Generate(r)

			// Try to get from cache
			cached, found := cfg.Store.Get(key)
			if found && !cached.IsExpired() {
				// Check conditional request
				if cfg.EnableConditionalRequests && cached.ETag != "" {
					if r.Header.Get("If-None-Match") == cached.ETag {
						w.WriteHeader(http.StatusNotModified)
						if cfg.OnCacheHit != nil {
							cfg.OnCacheHit(key, cached)
						}
						return
					}
				}

				// Cache hit - write cached response
				writeCachedResponse(w, cached)
				if cfg.OnCacheHit != nil {
					cfg.OnCacheHit(key, cached)
				}
				return
			}

			// Cache miss - call backend and cache response
			if cfg.OnCacheMiss != nil {
				cfg.OnCacheMiss(key)
			}

			// Create response recorder
			recorder := httputil.NewResponseRecorder(w)

			// Execute next handler
			next.ServeHTTP(recorder, r)

			// Check if response is cacheable
			if !isCacheableStatus(recorder.StatusCode(), cfg.StatusCodes) {
				return
			}

			// Check response size
			bodySize := int64(len(recorder.Body()))
			if bodySize > cfg.MaxSize {
				return
			}

			// Check Cache-Control from response
			ttl := cfg.DefaultTTL
			if cfg.RespectCacheControl {
				if respTTL, noStore := parseCacheControl(recorder.Header().Get("Cache-Control")); noStore {
					// Don't cache if no-store directive
					return
				} else if respTTL > 0 {
					ttl = respTTL
				}
			}

			// Generate ETag if enabled
			etag := ""
			if cfg.EnableConditionalRequests {
				etag = generateETag(recorder.Body())
			}

			// Create cached response
			cached = &CachedResponse{
				StatusCode: recorder.StatusCode(),
				Header:     recorder.Header().Clone(),
				Body:       recorder.Body(),
				CachedAt:   time.Now(),
				ExpiresAt:  time.Now().Add(ttl),
				ETag:       etag,
				Size:       bodySize,
			}

			// Add ETag header
			if etag != "" {
				cached.Header.Set("ETag", etag)
			}

			// Store in cache
			cfg.Store.Set(key, cached, ttl)
		})
	}
}

// IsExpired checks if cached response has expired
func (cr *CachedResponse) IsExpired() bool {
	return time.Now().After(cr.ExpiresAt)
}

// writeCachedResponse writes a cached response to the client
func writeCachedResponse(w http.ResponseWriter, cached *CachedResponse) {
	// Write headers
	for key, values := range cached.Header {
		for _, value := range values {
			w.Header().Add(key, value)
		}
	}

	// Add cache indicator header
	w.Header().Set("X-Cache", "HIT")
	w.Header().Set("Age", strconv.FormatInt(int64(time.Since(cached.CachedAt).Seconds()), 10))

	// Write status code
	w.WriteHeader(cached.StatusCode)

	// Write body
	if cached.Body != nil {
		_, _ = w.Write(cached.Body)
	}
}

// isCacheableMethod checks if the HTTP method is cacheable
func isCacheableMethod(method string, allowed []string) bool {
	for _, m := range allowed {
		if strings.EqualFold(method, m) {
			return true
		}
	}
	return false
}

// isCacheableStatus checks if the status code is cacheable
func isCacheableStatus(code int, allowed []int) bool {
	for _, c := range allowed {
		if code == c {
			return true
		}
	}
	return false
}

// hasNoCacheDirective checks if request has no-cache directive
func hasNoCacheDirective(r *http.Request) bool {
	cacheControl := r.Header.Get("Cache-Control")
	if cacheControl == "" {
		return false
	}

	directives := strings.Split(cacheControl, ",")
	for _, directive := range directives {
		directive = strings.TrimSpace(strings.ToLower(directive))
		if directive == "no-cache" || directive == "no-store" {
			return true
		}
	}

	return false
}

// parseCacheControl parses Cache-Control header from response
// Returns TTL and whether no-store directive is present
func parseCacheControl(header string) (time.Duration, bool) {
	if header == "" {
		return 0, false
	}

	directives := strings.Split(header, ",")
	for _, directive := range directives {
		directive = strings.TrimSpace(strings.ToLower(directive))

		// Check for no-store
		if directive == "no-store" {
			return 0, true
		}

		// Check for max-age
		if strings.HasPrefix(directive, "max-age=") {
			maxAgeStr := strings.TrimPrefix(directive, "max-age=")
			if seconds, err := strconv.Atoi(maxAgeStr); err == nil && seconds > 0 {
				return time.Duration(seconds) * time.Second, false
			}
		}
	}

	return 0, false
}

// generateETag generates an ETag for response body
func generateETag(body []byte) string {
	h := fnv.New64a()
	h.Write(body)
	return fmt.Sprintf(`"%x"`, h.Sum64())
}

// DefaultKeyStrategy implements default cache key generation
type DefaultKeyStrategy struct {
	// IncludeHeaders is the list of headers to include in key
	// Default: ["Accept", "Accept-Encoding"]
	IncludeHeaders []string
}

// NewDefaultKeyStrategy creates a default key strategy
func NewDefaultKeyStrategy() *DefaultKeyStrategy {
	return &DefaultKeyStrategy{
		IncludeHeaders: []string{"Accept", "Accept-Encoding"},
	}
}

// Generate generates a cache key from request
func (s *DefaultKeyStrategy) Generate(r *http.Request) string {
	h := fnv.New64a()

	// Method
	h.Write([]byte(r.Method))
	h.Write([]byte("|"))

	// URL (full path with query)
	h.Write([]byte(r.URL.String()))
	h.Write([]byte("|"))

	// Headers
	for _, header := range s.IncludeHeaders {
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

// CustomKeyStrategy allows custom key generation
type CustomKeyStrategy struct {
	// KeyFunc is a custom function to generate cache keys
	KeyFunc func(r *http.Request) string
}

// Generate generates a cache key using custom function
func (s *CustomKeyStrategy) Generate(r *http.Request) string {
	if s.KeyFunc != nil {
		return s.KeyFunc(r)
	}
	return NewDefaultKeyStrategy().Generate(r)
}

// Stats returns cache statistics
func Stats(store Store) StoreStats {
	return store.Stats()
}
