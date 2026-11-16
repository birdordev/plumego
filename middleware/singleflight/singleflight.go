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
//	import "github.com/birdordev/plumego/middleware/singleflight"
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
	"io"
	"net"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/birdordev/plumego/contract"
	internaltransport "github.com/birdordev/plumego/internal/httputil"
)

var errLeaderWrite = errors.New("coalesced leader response write failed")
var errWaiterWrite = errors.New("coalesced waiter response write failed")
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
	// KeyFunc generates coalescing keys from requests. A nil value uses
	// [DefaultKeyFunc], which includes the method, host, URL, and selected headers.
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
	statusCode     int
	header         http.Header
	addedValues    http.Header
	deletedHeaders []string
	body           []byte
	trailers       http.Header
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
	ctx := r.Context()
	timer := time.NewTimer(c.config.Timeout)
	defer timer.Stop()

	timedOut := false
	select {
	case <-inflight.done:
	case <-timer.C:
		timedOut = true
	case <-ctx.Done():
		c.decrementWaiters(inflight)
		return
	}

	// Completion or timeout can win the select even if cancellation is ready.
	if ctx.Err() != nil {
		c.decrementWaiters(inflight)
		return
	}
	if timedOut {
		c.decrementWaiters(inflight)
		internaltransport.WriteTransportError(w, r, http.StatusGatewayTimeout, contract.CodeTimeout, "upstream request timeout", nil)
		return
	}
	if inflight.err != nil {
		c.failWaiter(w, r, key, inflight.err)
		return
	}
	if inflight.response == nil {
		c.failWaiter(w, r, key, errUpstreamPanic)
		return
	}

	if err := writeResponse(w, r, inflight.response, c.config.AddCoalescedHeader); err != nil {
		// The response is already committed. Report without forwarding raw writer errors.
		c.reportError(key, errWaiterWrite)
		return
	}
	c.reportCoalesced(key, 1)
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

	// Commit implicit success headers even when the handler wrote no body.
	if !recorder.wroteHeader {
		recorder.WriteHeader(http.StatusOK)
	}
	var trailers http.Header
	if !recorder.hijacked {
		trailers = internaltransport.CopyResponseTrailers(nil, recorder.committed, recorder.header)
		if len(trailers) > 0 {
			internaltransport.CopyHeaders(w.Header(), trailers)
		}
	}

	if recorder.writeFailed {
		inflight.err = errLeaderWrite
		return
	}
	if recorder.Overflowed() {
		inflight.err = errResponseTooLarge
		return
	}
	if recorder.Unreplayable() {
		inflight.err = errUnreplayableResponse
		return
	}

	// Capture response
	inflight.response = recorder.CapturedResponse()
	inflight.response.trailers = trailers
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
func writeResponse(w http.ResponseWriter, r *http.Request, resp *capturedResponse, addCoalescedHeader bool) error {
	header := w.Header()
	for _, key := range resp.deletedHeaders {
		delete(header, key)
	}
	internaltransport.CopyHeaders(header, resp.header)
	for key, values := range resp.addedValues {
		header[key] = append(header[key], values...)
	}
	if addCoalescedHeader {
		header.Set("X-Coalesced", "true")
	}
	internaltransport.EnsureNoSniff(header)
	w.WriteHeader(resp.statusCode)
	if r.Method == http.MethodHead {
		return nil
	}
	var err error
	if resp.body != nil {
		var n int
		n, err = w.Write(resp.body)
		if err == nil && n != len(resp.body) {
			err = io.ErrShortWrite
		}
	}
	if len(resp.trailers) > 0 {
		internaltransport.CopyHeaders(w.Header(), resp.trailers)
	}
	return err
}

type limitedResponseRecorder struct {
	http.ResponseWriter
	statusCode   int
	header       http.Header
	initial      http.Header
	committed    http.Header
	body         []byte
	maxBytes     int
	wroteHeader  bool
	overflow     bool
	unreplayable bool
	writeFailed  bool
	hijacked     bool
}

func newLimitedResponseRecorder(w http.ResponseWriter, maxBytes int) *limitedResponseRecorder {
	initial := w.Header().Clone()
	header := initial.Clone()
	if header == nil {
		header = make(http.Header)
	}
	return &limitedResponseRecorder{
		ResponseWriter: w,
		statusCode:     http.StatusOK,
		header:         header,
		initial:        initial,
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
		internaltransport.ReplaceHeaders(dst, r.header)
		r.ResponseWriter.WriteHeader(statusCode)
		return
	}
	r.statusCode = statusCode
	r.wroteHeader = true
	r.committed = r.header.Clone()
	dst := r.ResponseWriter.Header()
	internaltransport.ReplaceHeaders(dst, r.committed)
	internaltransport.EnsureNoSniff(dst)
	r.ResponseWriter.WriteHeader(statusCode)
}

func (r *limitedResponseRecorder) Write(p []byte) (int, error) {
	if !r.wroteHeader {
		r.WriteHeader(http.StatusOK)
	}
	n, err := internaltransport.SafeWrite(r.ResponseWriter, p)
	if err == nil && n != len(p) {
		err = io.ErrShortWrite
	}
	if err != nil && !r.writeFailed {
		r.writeFailed = true
		r.body = nil
	}
	if !r.overflow && !r.writeFailed {
		if r.maxBytes > 0 && len(r.body)+len(p) > r.maxBytes {
			r.overflow = true
			r.body = nil
		} else {
			r.body = append(r.body, p...)
		}
	}
	return n, err
}

func (r *limitedResponseRecorder) FlushError() error {
	var original http.Header
	var dst http.Header
	if !r.wroteHeader {
		dst = r.ResponseWriter.Header()
		original = dst.Clone()
		internaltransport.ReplaceHeaders(dst, r.header)
		internaltransport.EnsureNoSniff(dst)
	}
	err := http.NewResponseController(r.ResponseWriter).Flush()
	if errors.Is(err, http.ErrNotSupported) {
		if !r.wroteHeader {
			internaltransport.ReplaceHeaders(dst, original)
		}
		return err
	}
	if !r.wroteHeader {
		r.committed = r.header.Clone()
		r.wroteHeader = true
	}
	r.unreplayable = true
	return err
}

func (r *limitedResponseRecorder) Flush() { _ = r.FlushError() }

func (r *limitedResponseRecorder) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	conn, rw, err := internaltransport.HijackIfSupported(r.ResponseWriter)
	if err != nil {
		return nil, nil, err
	}
	r.wroteHeader = true
	r.unreplayable = true
	r.hijacked = true
	return conn, rw, nil
}

func (r *limitedResponseRecorder) Overflowed() bool {
	return r.overflow
}

func (r *limitedResponseRecorder) Unreplayable() bool {
	return r.unreplayable
}

func (r *limitedResponseRecorder) CapturedResponse() *capturedResponse {
	final := r.committed
	if final == nil {
		final = r.header
	}
	response := &capturedResponse{
		statusCode: r.statusCode,
		body:       r.body,
	}
	for key, values := range final {
		original, exists := r.initial[key]
		if exists && slices.Equal(original, values) {
			continue
		}
		// A retained prefix belongs to the leader's outer middleware.
		if len(original) > 0 && len(values) > len(original) && slices.Equal(original, values[:len(original)]) {
			if response.addedValues == nil {
				response.addedValues = make(http.Header)
			}
			response.addedValues[key] = slices.Clone(values[len(original):])
			continue
		}
		if response.header == nil {
			response.header = make(http.Header)
		}
		response.header[key] = slices.Clone(values)
	}
	for key := range r.initial {
		if _, exists := final[key]; !exists {
			response.deletedHeaders = append(response.deletedHeaders, key)
		}
	}
	return response
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

// DefaultKeyFunc generates a key from the request method, host, URL string, and
// the Accept, Accept-Encoding, Accept-Language, Authorization, Cookie, and Range
// header values.
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

// HeaderAwareKeyFunc generates keys using a snapshot of the supplied header names.
func HeaderAwareKeyFunc(headers []string) KeyFunc {
	headers = slices.Clone(headers)
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
