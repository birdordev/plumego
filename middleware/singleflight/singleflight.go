// Package singleflight provides request coalescing middleware.
//
// The middleware deduplicates identical in-flight safe HTTP requests to reduce
// downstream transport load. When multiple concurrent requests share the same
// coalesce key, only the leader request is forwarded to the next handler.
// Other waiters receive a bounded replay of the leader response.
//
// This is the HTTP middleware adaptation of the singleflight pattern: use it only
// for bounded, safe, non-streaming responses whose variants are fully captured
// by the coalesce key.
//
// Singleflight is not a cache and does not own business freshness policy. It only
// shares the response for requests that are already in flight. The default key
// is not a security isolation boundary; sensitive response variants should use
// an explicit KeyFunc.
//
// Example usage:
//
//	import "github.com/spcent/plumego/middleware/singleflight"
//
//	// Simple coalescing with defaults
//	mw := singleflight.New(singleflight.Config{}).Middleware()
//
//	// Advanced configuration
//	mw = singleflight.New(singleflight.Config{
//		KeyFunc: singleflight.DefaultKeyFunc,
//		Methods: []string{"GET", "HEAD"},
//		OnCoalesced: func(key string, count int) {
//			log.Printf("Coalesced request event for key %s", key)
//		},
//	}).Middleware()
//	_ = mw
package singleflight

import (
	"bufio"
	"errors"
	"hash"
	"hash/fnv"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/spcent/plumego/contract"
	internaltransport "github.com/spcent/plumego/internal/httputil"
)

var errUpstreamPanic = errors.New("upstream request panicked")
var errResponseTooLarge = errors.New("coalesced response exceeded capture limit")
var errUnreplayableResponse = errors.New("coalesced response used unreplayable transport operation")

const defaultMaxResponseBytes = 10 << 20

var defaultKeyHeaders = []string{
	"Accept",
	"Accept-Encoding",
	"Accept-Language",
	"Authorization",
	"Cookie",
	"Range",
}

// KeyFunc generates a unique key for request deduplication
type KeyFunc func(r *http.Request) string

// Config holds coalescing middleware configuration
type Config struct {
	// KeyFunc generates cache keys from requests
	// Default: DefaultKeyFunc (Method + URL)
	KeyFunc KeyFunc

	// Methods is the list of HTTP methods to coalesce
	// Only safe, idempotent methods should be coalesced
	// Default: ["GET", "HEAD"]
	Methods []string

	// Timeout is the maximum time to wait for an in-flight request
	// If the upstream request takes longer, subsequent requests will
	// time out instead of waiting indefinitely.
	// Default: 30 seconds
	Timeout time.Duration

	// MaxResponseBytes is the maximum leader response body size captured for
	// replay to coalesced waiters. The leader still receives the full response.
	// Default: 10MB
	MaxResponseBytes int

	// OnCoalesced is called once for each waiter that successfully receives a
	// coalesced response. The count parameter is the per-callback event count
	// and is currently always 1; aggregate in the caller when totals are needed.
	OnCoalesced func(key string, count int)

	// OnError is called when an error occurs during coalescing (optional)
	OnError func(key string, err error)

	// AddCoalescedHeader, when true, sets the X-Coalesced: true response header
	// on replies that were served from a coalesced in-flight response.
	// The header is opt-in: it is never set by default because it leaks
	// infrastructure topology to end-clients and can interfere with CDN caches.
	// Enable it only for internal service-to-service paths where the information
	// is useful for debugging or observability.
	AddCoalescedHeader bool
}

// WithDefaults returns a Config with default values applied
func (c *Config) WithDefaults() *Config {
	config := *c

	if config.KeyFunc == nil {
		config.KeyFunc = DefaultKeyFunc
	}

	config.Methods = normalizeMethods(config.Methods)
	if len(config.Methods) == 0 {
		config.Methods = []string{"GET", "HEAD"}
	}

	if config.Timeout == 0 {
		config.Timeout = 30 * time.Second
	}
	if config.MaxResponseBytes <= 0 {
		config.MaxResponseBytes = defaultMaxResponseBytes
	}

	return &config
}

// Coalescer manages in-flight request deduplication
type Coalescer struct {
	mu       sync.RWMutex
	inFlight map[string]*inFlightRequest
	config   *Config
}

// inFlightRequest represents an in-flight request being processed
type inFlightRequest struct {
	response *capturedResponse
	err      error
	done     chan struct{}
	waiters  atomic.Int32
}

// capturedResponse represents a captured HTTP response
type capturedResponse struct {
	statusCode int
	header     http.Header
	body       []byte
}

// New creates a new request coalescer with the provided configuration.
//
// The returned Coalescer is safe for concurrent use. Call its Middleware()
// method to obtain the middleware function. Use the zero Config{} for
// package defaults (GET/HEAD coalescing, 30-second wait, 10MB capture).
func New(config Config) *Coalescer {
	cfg := config.WithDefaults()

	return &Coalescer{
		inFlight: make(map[string]*inFlightRequest),
		config:   cfg,
	}
}

// Middleware returns the middleware function
func (c *Coalescer) Middleware() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Only coalesce safe methods
			if !c.isSafeMethod(r.Method) {
				next.ServeHTTP(w, r)
				return
			}

			// Generate request key
			key := c.config.KeyFunc(r)
			if strings.TrimSpace(key) == "" {
				next.ServeHTTP(w, r)
				return
			}

			// Check if request is already in-flight (single critical section)
			c.mu.Lock()
			inflight, exists := c.inFlight[key]
			if exists {
				inflight.waiters.Add(1)
				c.mu.Unlock()

				// Wait for in-flight request to complete
				c.waitForInFlight(w, r, key, inflight)
				return
			}

			// Start new request
			inflight = &inFlightRequest{
				done: make(chan struct{}),
			}
			c.inFlight[key] = inflight
			c.mu.Unlock()

			c.executeRequest(w, r, key, inflight, next)
		})
	}
}

// waitForInFlight waits for an in-flight request to complete
func (c *Coalescer) waitForInFlight(w http.ResponseWriter, r *http.Request, key string, inflight *inFlightRequest) {
	// Wait for completion or timeout
	timer := time.NewTimer(c.config.Timeout)
	defer timer.Stop()

	select {
	case <-inflight.done:
		// Request completed - write cached response
		if inflight.err != nil {
			c.failWaiter(w, r, key, inflight.err)
			return
		}
		if inflight.response == nil {
			c.failWaiter(w, r, key, errUpstreamPanic)
			return
		}

		writeResponse(w, r, inflight.response, c.config.AddCoalescedHeader)

		// Call hook
		c.reportCoalesced(key, 1)

	case <-timer.C:
		// Timeout. Do not wait indefinitely for a slow upstream leader.
		c.decrementWaiters(inflight)

		internaltransport.WriteTransportError(w, r, http.StatusGatewayTimeout, contract.CodeTimeout, "upstream request timeout", nil)
	case <-r.Context().Done():
		c.decrementWaiters(inflight)
		return
	}
}

func (c *Coalescer) failWaiter(w http.ResponseWriter, r *http.Request, key string, err error) {
	c.reportError(key, err)
	internaltransport.WriteTransportError(w, r, http.StatusBadGateway, contract.CodeUpstreamFailed, "upstream request failed", nil)
}

func (c *Coalescer) reportError(key string, err error) {
	if c.config.OnError == nil {
		return
	}
	defer func() {
		_ = recover()
	}()
	c.config.OnError(key, err)
}

func (c *Coalescer) reportCoalesced(key string, count int) {
	if c.config.OnCoalesced == nil {
		return
	}
	defer func() {
		_ = recover()
	}()
	c.config.OnCoalesced(key, count)
}

// decrementWaiters decrements the waiter count using a CAS loop to avoid
// underflow during concurrent timeout and normal completion races.
func (c *Coalescer) decrementWaiters(inflight *inFlightRequest) {
	for {
		v := inflight.waiters.Load()
		if v <= 0 {
			return
		}
		if inflight.waiters.CompareAndSwap(v, v-1) {
			return
		}
	}
}

// executeRequest executes a new request and broadcasts to waiters
func (c *Coalescer) executeRequest(w http.ResponseWriter, r *http.Request, key string, inflight *inFlightRequest, next http.Handler) {
	// Create response recorder
	recorder := newLimitedResponseRecorder(w, c.config.MaxResponseBytes)

	defer func() {
		if rec := recover(); rec != nil {
			inflight.err = errUpstreamPanic
			c.finishRequest(key, inflight)
			panic(rec)
		}
		c.finishRequest(key, inflight)
	}()

	// Execute request
	next.ServeHTTP(recorder, r)

	if recorder.Overflowed() {
		inflight.err = errResponseTooLarge
		return
	}
	if recorder.Unreplayable() {
		inflight.err = errUnreplayableResponse
		return
	}

	// Commit implicit success headers even when the handler wrote no body.
	if !recorder.wroteHeader {
		recorder.WriteHeader(http.StatusOK)
	}

	// Capture response
	inflight.response = recorder.CapturedResponse()
}

func (c *Coalescer) finishRequest(key string, inflight *inFlightRequest) {
	c.mu.Lock()
	delete(c.inFlight, key)
	c.mu.Unlock()
	close(inflight.done)
}

// isSafeMethod checks if the HTTP method is safe to coalesce
func (c *Coalescer) isSafeMethod(method string) bool {
	for _, m := range c.config.Methods {
		if method == m {
			return true
		}
	}
	return false
}

// writeResponse writes a captured response to the client.
// addCoalescedHeader controls whether the X-Coalesced response header is set.
func writeResponse(w http.ResponseWriter, r *http.Request, resp *capturedResponse, addCoalescedHeader bool) {
	internaltransport.ReplaceHeaders(w.Header(), resp.header)
	if addCoalescedHeader {
		w.Header().Set("X-Coalesced", "true")
	}
	w.WriteHeader(resp.statusCode)
	if r.Method != http.MethodHead && resp.body != nil {
		_, _ = w.Write(resp.body)
	}
}

type limitedResponseRecorder struct {
	http.ResponseWriter
	statusCode   int
	header       http.Header
	committed    http.Header
	body         []byte
	maxBytes     int
	wroteHeader  bool
	overflow     bool
	unreplayable bool
}

func newLimitedResponseRecorder(w http.ResponseWriter, maxBytes int) *limitedResponseRecorder {
	return &limitedResponseRecorder{
		ResponseWriter: w,
		statusCode:     http.StatusOK,
		header:         make(http.Header),
		maxBytes:       maxBytes,
	}
}

func (r *limitedResponseRecorder) Unwrap() http.ResponseWriter {
	return r.ResponseWriter
}

func (r *limitedResponseRecorder) Header() http.Header {
	return r.header
}

func (r *limitedResponseRecorder) WriteHeader(statusCode int) {
	if r.wroteHeader {
		return
	}
	if statusCode < 100 || statusCode > 999 {
		panic("invalid WriteHeader code")
	}
	if statusCode < 200 && statusCode != http.StatusSwitchingProtocols {
		dst := r.ResponseWriter.Header()
		original := dst.Clone()
		defer internaltransport.ReplaceHeaders(dst, original)
		internaltransport.CopyHeaders(dst, r.header)
		r.ResponseWriter.WriteHeader(statusCode)
		return
	}
	r.statusCode = statusCode
	r.wroteHeader = true
	r.committed = r.header.Clone()
	internaltransport.CommitHeadersCopy(r.ResponseWriter, r.header, statusCode)
}

func (r *limitedResponseRecorder) Write(p []byte) (int, error) {
	if !r.wroteHeader {
		r.WriteHeader(http.StatusOK)
	}
	if !r.overflow {
		if r.maxBytes > 0 && len(r.body)+len(p) > r.maxBytes {
			r.overflow = true
			r.body = nil
		} else {
			r.body = append(r.body, p...)
		}
	}
	return internaltransport.SafeWrite(r.ResponseWriter, p)
}

func (r *limitedResponseRecorder) Flush() {
	r.unreplayable = true
	if !r.wroteHeader {
		r.WriteHeader(http.StatusOK)
	}
	internaltransport.FlushIfSupported(r.ResponseWriter)
}

func (r *limitedResponseRecorder) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	conn, rw, err := internaltransport.HijackIfSupported(r.ResponseWriter)
	if err != nil {
		return nil, nil, err
	}
	r.wroteHeader = true
	r.unreplayable = true
	return conn, rw, nil
}

func (r *limitedResponseRecorder) Overflowed() bool {
	return r.overflow
}

func (r *limitedResponseRecorder) Unreplayable() bool {
	return r.unreplayable
}

func (r *limitedResponseRecorder) CapturedResponse() *capturedResponse {
	header := r.header.Clone()
	if r.committed != nil {
		header = r.committed.Clone()
	}
	return &capturedResponse{
		statusCode: r.statusCode,
		header:     header,
		body:       r.body,
	}
}

func normalizeMethods(methods []string) []string {
	normalized := make([]string, 0, len(methods))
	for _, method := range methods {
		method = strings.ToUpper(strings.TrimSpace(method))
		if method == "" {
			continue
		}
		normalized = append(normalized, method)
	}
	return normalized
}

// DefaultKeyFunc generates a key from method and URL
func DefaultKeyFunc(r *http.Request) string {
	h := fnv.New64a()
	h.Write([]byte(r.Method))
	h.Write([]byte("|"))
	h.Write([]byte(r.Host))
	h.Write([]byte("|"))
	h.Write([]byte(r.URL.String()))
	h.Write([]byte("|"))
	for _, header := range defaultKeyHeaders {
		writeHeaderKey(h, r, header)
	}
	return strconv.FormatUint(h.Sum64(), 16)
}

// HeaderAwareKeyFunc generates a key including specific headers
func HeaderAwareKeyFunc(headers []string) KeyFunc {
	return func(r *http.Request) string {
		h := fnv.New64a()

		h.Write([]byte(r.Method))
		h.Write([]byte("|"))
		h.Write([]byte(r.Host))
		h.Write([]byte("|"))
		h.Write([]byte(r.URL.String()))
		h.Write([]byte("|"))

		for _, header := range headers {
			writeHeaderKey(h, r, header)
		}

		return strconv.FormatUint(h.Sum64(), 16)
	}
}

func writeHeaderKey(h hash.Hash64, r *http.Request, header string) {
	if r == nil {
		return
	}
	header = http.CanonicalHeaderKey(strings.TrimSpace(header))
	if header == "" {
		return
	}

	values := r.Header.Values(header)
	if len(values) == 0 {
		return
	}

	h.Write([]byte(strings.ToLower(header)))
	h.Write([]byte(":"))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		h.Write([]byte(value))
		h.Write([]byte("\x00"))
	}
	h.Write([]byte("|"))
}

// Stats returns coalescing statistics
type Stats struct {
	InFlight int
}

// Stats returns current coalescing statistics
func (c *Coalescer) Stats() Stats {
	c.mu.RLock()
	defer c.mu.RUnlock()

	return Stats{
		InFlight: len(c.inFlight),
	}
}
