// Package nethttp provides a production-ready HTTP client with automatic retry and backoff.
//
// The client wraps the standard library's http.Client with:
//   - Automatic retry for network errors and 5xx responses
//   - Configurable retry policies (timeout-only, status-code-based, composite, custom)
//   - Exponential backoff with jitter to prevent thundering herd
//   - Per-request timeout and retry overrides
//   - Request body replay for safe retries (GET, PUT, DELETE and POST with GetBody)
//   - SSRF protection via URL validation before each outbound request
//
// Example usage:
//
//	import nethttp "github.com/spcent/plumego/internal/nethttp"
//
//	client := nethttp.New(
//	    nethttp.WithTimeout(10 * time.Second),
//	    nethttp.WithRetryCount(3),
//	    nethttp.WithRetryPolicy(nethttp.StatusCodeRetryPolicy{Codes: []int{500, 502, 503}}),
//	    nethttp.WithDefaultSSRFProtection(),
//	)
//
//	body, err := client.Get(ctx, "https://api.example.com/data")
//	if err != nil {
//	    // All retries failed
//	}
package nethttp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"math/rand"
	"net"
	"net/http"
	"time"
)

// ErrNilRequest is returned when a nil HTTP request is passed to Do.
var ErrNilRequest = errors.New("nethttp: nil request")

// ErrResponseTooLarge is returned when a response body exceeds the client's
// configured maximum response size.
var ErrResponseTooLarge = errors.New("nethttp: response body exceeds max response size")

// RetryPolicy determines whether an HTTP request should be retried.
//
// Implementations receive the response (may be nil on network error), the error
// (may be nil when a response was received), and the zero-based attempt index.
type RetryPolicy interface {
	ShouldRetry(resp *http.Response, err error, attempt int) bool
}

// TimeoutRetryPolicy retries only on timeout errors.
type TimeoutRetryPolicy struct{}

func (p TimeoutRetryPolicy) ShouldRetry(resp *http.Response, err error, attempt int) bool {
	return isTimeoutError(err)
}

// StatusCodeRetryPolicy retries when the response status code is in the configured list.
type StatusCodeRetryPolicy struct {
	Codes []int
}

func (p StatusCodeRetryPolicy) ShouldRetry(resp *http.Response, err error, attempt int) bool {
	if resp == nil {
		return false
	}
	for _, code := range p.Codes {
		if resp.StatusCode == code {
			return true
		}
	}
	return false
}

// CompositeRetryPolicy combines multiple policies with OR logic: retries if any policy says so.
type CompositeRetryPolicy struct {
	Policies []RetryPolicy
}

func (p CompositeRetryPolicy) ShouldRetry(resp *http.Response, err error, attempt int) bool {
	for _, policy := range p.Policies {
		if policy == nil {
			continue
		}
		if policy.ShouldRetry(resp, err, attempt) {
			return true
		}
	}
	return false
}

// AlwaysRetryPolicy retries on any error or 5xx response.
type AlwaysRetryPolicy struct{}

func (p AlwaysRetryPolicy) ShouldRetry(resp *http.Response, err error, attempt int) bool {
	return err != nil || (resp != nil && resp.StatusCode >= 500)
}

// RoundTripperFunc is a function that implements the core HTTP transport contract.
type RoundTripperFunc func(req *http.Request) (*http.Response, error)

// Client is a wrapper around http.Client with retry, timeout, backoff, and middleware support.
type Client struct {
	client           *http.Client
	retryCount       int
	retryWait        time.Duration
	maxRetryWait     time.Duration
	retryPolicy      RetryPolicy
	defaultTimeout   time.Duration
	retryCheck       func(*http.Request) bool
	ssrfProtection   *SSRFProtection
	enableSSRFCheck  bool
	maxResponseBytes int64
}

// defaultMaxResponseBytes bounds successful response bodies read into memory
// by default. Configure with WithMaxResponseBytes.
const defaultMaxResponseBytes int64 = 10 << 20 // 10 MiB

func (c *Client) setSSRFProtection(protection SSRFProtection) {
	c.ssrfProtection = &protection
	c.enableSSRFCheck = true
}

// Option is a functional option for configuring a Client.
type Option func(*Client)

// WithTimeout sets the default timeout for every request.
func WithTimeout(timeout time.Duration) Option {
	return func(c *Client) {
		c.client.Timeout = timeout
		c.defaultTimeout = timeout
	}
}

// WithRetryCount sets the maximum number of retry attempts (not counting the initial attempt).
func WithRetryCount(count int) Option {
	return func(c *Client) {
		c.retryCount = normalizeRetryCount(count)
	}
}

// WithRetryWait sets the base duration between retries (before jitter).
func WithRetryWait(wait time.Duration) Option {
	return func(c *Client) {
		c.retryWait = wait
	}
}

// WithMaxRetryWait caps the maximum computed backoff duration.
func WithMaxRetryWait(max time.Duration) Option {
	return func(c *Client) {
		c.maxRetryWait = max
	}
}

// WithRetryPolicy sets the retry decision policy for the client.
func WithRetryPolicy(policy RetryPolicy) Option {
	return func(c *Client) {
		c.retryPolicy = policy
	}
}

// WithRetryCheck sets a predicate that, when it returns false for a given request,
// disables retries entirely for that request (e.g., to skip retries on POST).
func WithRetryCheck(check func(*http.Request) bool) Option {
	return func(c *Client) {
		c.retryCheck = check
	}
}

// WithTransport replaces the underlying http.RoundTripper.
func WithTransport(transport http.RoundTripper) Option {
	return func(c *Client) {
		c.client.Transport = transport
	}
}

// WithSSRFProtection enables SSRF URL validation using the given configuration.
func WithSSRFProtection(protection SSRFProtection) Option {
	return func(c *Client) {
		c.setSSRFProtection(protection)
	}
}

// WithDefaultSSRFProtection enables SSRF protection with secure defaults
// (blocks private IPs, loopback, link-local; allows only http and https schemes).
func WithDefaultSSRFProtection() Option {
	return func(c *Client) {
		c.setSSRFProtection(DefaultSSRFProtection())
	}
}

// WithMaxResponseBytes caps how many bytes of a successful response body are
// read into memory. Responses larger than the limit fail with
// ErrResponseTooLarge instead of being silently truncated. A value of zero
// disables the limit.
func WithMaxResponseBytes(maxBytes int64) Option {
	return func(c *Client) {
		c.maxResponseBytes = maxBytes
	}
}

// New creates a new Client with the provided options.
// Default: 3 s timeout, up to 3 retries with 1 s base wait capped at 5 s, timeout-only retry policy.
func New(opts ...Option) *Client {
	c := &Client{
		client: &http.Client{
			Timeout: 3 * time.Second,
		},
		retryCount:       3,
		retryWait:        1 * time.Second,
		maxRetryWait:     5 * time.Second,
		retryPolicy:      TimeoutRetryPolicy{},
		defaultTimeout:   3 * time.Second,
		maxResponseBytes: defaultMaxResponseBytes,
	}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

// requestConfig holds per-request overrides applied on top of client defaults.
type requestConfig struct {
	retryCount  *int
	retryPolicy RetryPolicy
	timeout     *time.Duration
	headers     map[string]string
	retryCheck  func(*http.Request) bool
}

// RequestOption is a functional option for a single request.
type RequestOption func(*requestConfig)

// WithRequestTimeout overrides the timeout for a single request.
func WithRequestTimeout(timeout time.Duration) RequestOption {
	return func(c *requestConfig) { c.timeout = &timeout }
}

// WithRequestRetryCount overrides the retry count for a single request.
func WithRequestRetryCount(count int) RequestOption {
	return func(c *requestConfig) {
		count = normalizeRetryCount(count)
		c.retryCount = &count
	}
}

// WithRequestRetryPolicy overrides the retry policy for a single request.
func WithRequestRetryPolicy(p RetryPolicy) RequestOption {
	return func(c *requestConfig) { c.retryPolicy = p }
}

// WithRequestRetryCheck overrides the retry check predicate for a single request.
func WithRequestRetryCheck(check func(*http.Request) bool) RequestOption {
	return func(c *requestConfig) { c.retryCheck = check }
}

// WithHeader adds (or overrides) a header for a single request.
func WithHeader(key, val string) RequestOption {
	return func(cfg *requestConfig) {
		cfg.headers[key] = val
	}
}

// isTimeoutError reports whether err represents a timeout condition.
func isTimeoutError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return true
	}
	return false
}

// backoffWithJitter computes exponential backoff capped at max with ±50% jitter.
func backoffWithJitter(base time.Duration, attempt int, max time.Duration) time.Duration {
	backoff := float64(base) * math.Pow(2, float64(attempt))
	if backoff > float64(max) {
		backoff = float64(max)
	}
	jitterFactor := 0.5 + rand.Float64() // [0.5, 1.5)
	return time.Duration(backoff * jitterFactor)
}

func waitBackoff(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return nil
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func normalizeRetryCount(count int) int {
	if count < 0 {
		return 0
	}
	return count
}

// doRequest is the internal dispatcher: applies SSRF check, builds the request config,
// composes the middleware chain, and delegates to do().
func (c *Client) doRequest(req *http.Request, opts ...RequestOption) (*http.Response, error) {
	if req == nil {
		return nil, ErrNilRequest
	}

	if c.enableSSRFCheck && c.ssrfProtection != nil {
		if err := ValidateURL(req.URL.String(), *c.ssrfProtection); err != nil {
			return nil, err
		}
	}

	cfg := &requestConfig{
		retryCount:  &c.retryCount,
		retryPolicy: c.retryPolicy,
		timeout:     &c.defaultTimeout,
		headers:     make(map[string]string),
		retryCheck:  c.retryCheck,
	}
	for _, opt := range opts {
		opt(cfg)
	}

	if cfg.retryCheck != nil && !cfg.retryCheck(req) {
		noRetry := 0
		cfg.retryCount = &noRetry
	}

	for k, v := range cfg.headers {
		req.Header.Set(k, v)
	}

	if err := ensureReplayableBody(req, cfg.retryCount); err != nil {
		return nil, err
	}

	return c.do(cfg)(req)
}

// cancelingBody wraps a response body and cancels the associated context when closed.
type cancelingBody struct {
	io.ReadCloser
	cancel context.CancelFunc
}

func (c *cancelingBody) Close() error {
	err := c.ReadCloser.Close()
	c.cancel()
	return err
}

// ensureReplayableBody buffers req.Body so that it can be re-read on retry.
// No-op when Body is nil, GetBody is already set, or retries are disabled.
func ensureReplayableBody(req *http.Request, retryCount *int) error {
	if req == nil || req.Body == nil || req.GetBody != nil {
		return nil
	}
	if retryCount == nil || *retryCount <= 0 {
		return nil
	}

	body, err := io.ReadAll(req.Body)
	if err != nil {
		return err
	}
	_ = req.Body.Close()

	req.Body = io.NopCloser(bytes.NewReader(body))
	req.GetBody = func() (io.ReadCloser, error) {
		return io.NopCloser(bytes.NewReader(body)), nil
	}
	return nil
}

// cloneRequest produces a shallow clone of req bound to ctx, replaying the body when possible.
func cloneRequest(ctx context.Context, req *http.Request) (*http.Request, error) {
	cloned := req.Clone(ctx)

	if req.Body == nil {
		return cloned, nil
	}
	if req.GetBody != nil {
		body, err := req.GetBody()
		if err != nil {
			return nil, err
		}
		cloned.Body = body
		return cloned, nil
	}
	cloned.Body = req.Body
	return cloned, nil
}

// do returns a RoundTripperFunc that executes req with retry logic and per-attempt timeouts.
func (c *Client) do(cfg *requestConfig) RoundTripperFunc {
	return func(req *http.Request) (*http.Response, error) {
		retryCount := 0
		if cfg.retryCount != nil {
			retryCount = *cfg.retryCount
		}

		for i := 0; i <= retryCount; i++ {
			ctx, cancel := context.WithTimeout(req.Context(), *cfg.timeout)
			attemptReq, err := cloneRequest(ctx, req)
			if err != nil {
				cancel()
				return nil, err
			}

			resp, reqErr := c.client.Do(attemptReq)

			// Success: response received and not a server error.
			if reqErr == nil && resp != nil && resp.StatusCode < 500 {
				resp.Body = &cancelingBody{ReadCloser: resp.Body, cancel: cancel}
				return resp, nil
			}

			shouldRetry := cfg.retryPolicy != nil && cfg.retryPolicy.ShouldRetry(resp, reqErr, i)
			if !shouldRetry || i == retryCount {
				// Final attempt or no retry: surface the error and cancel on close.
				if reqErr == nil && resp != nil && resp.StatusCode >= 500 {
					reqErr = errors.New("http error: " + resp.Status)
				}
				if resp != nil && resp.Body != nil {
					resp.Body = &cancelingBody{ReadCloser: resp.Body, cancel: cancel}
				} else {
					cancel()
				}
				return resp, reqErr
			}

			// Discard current response body before retrying.
			if resp != nil && resp.Body != nil {
				_ = resp.Body.Close()
			}
			cancel()

			if err := waitBackoff(req.Context(), backoffWithJitter(c.retryWait, i, c.maxRetryWait)); err != nil {
				return nil, err
			}
		}

		return nil, errors.New("http error: retries exhausted")
	}
}

// readResponse drains the body of a successful response and returns its bytes.
// It returns an error for any status >= 400. Bodies larger than the client's
// configured maximum response size fail with ErrResponseTooLarge rather than
// being silently truncated.
func (c *Client) readResponse(resp *http.Response) ([]byte, error) {
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode >= http.StatusBadRequest {
		return nil, errors.New("http error: " + resp.Status)
	}
	if c.maxResponseBytes <= 0 {
		return io.ReadAll(resp.Body)
	}
	// Read one byte past the limit so overflow is detectable.
	body, err := io.ReadAll(io.LimitReader(resp.Body, c.maxResponseBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read response body: %w", err)
	}
	if int64(len(body)) > c.maxResponseBytes {
		return nil, ErrResponseTooLarge
	}
	return body, nil
}

func (c *Client) doAndRead(req *http.Request, opts ...RequestOption) ([]byte, error) {
	resp, err := c.doRequest(req, opts...)
	if err != nil {
		if resp != nil && resp.Body != nil {
			_ = resp.Body.Close()
		}
		return nil, err
	}
	return c.readResponse(resp)
}

func (c *Client) doWithBody(ctx context.Context, method, url string, body []byte, contentType string, opts ...RequestOption) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, method, url, bytes.NewBuffer(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", contentType)
	return c.doAndRead(req, opts...)
}

// Get performs a GET request and returns the response body bytes.
func (c *Client) Get(ctx context.Context, url string, opts ...RequestOption) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	return c.doAndRead(req, opts...)
}

// Post performs a POST request with the given body and Content-Type header.
func (c *Client) Post(ctx context.Context, url string, body []byte, contentType string, opts ...RequestOption) ([]byte, error) {
	return c.doWithBody(ctx, http.MethodPost, url, body, contentType, opts...)
}

// PostJson marshals data as JSON and performs a POST request.
func (c *Client) PostJson(ctx context.Context, url string, data any, opts ...RequestOption) ([]byte, error) {
	body, err := json.Marshal(data)
	if err != nil {
		return nil, err
	}
	return c.Post(ctx, url, body, "application/json", opts...)
}

// Put performs a PUT request with the given body and Content-Type header.
func (c *Client) Put(ctx context.Context, url string, body []byte, contentType string, opts ...RequestOption) ([]byte, error) {
	return c.doWithBody(ctx, http.MethodPut, url, body, contentType, opts...)
}

// Patch performs a PATCH request with the given body and Content-Type header.
func (c *Client) Patch(ctx context.Context, url string, body []byte, contentType string, opts ...RequestOption) ([]byte, error) {
	return c.doWithBody(ctx, http.MethodPatch, url, body, contentType, opts...)
}

// Delete performs a DELETE request and returns the response body bytes.
func (c *Client) Delete(ctx context.Context, url string, opts ...RequestOption) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, url, nil)
	if err != nil {
		return nil, err
	}
	return c.doAndRead(req, opts...)
}

// Do executes a fully configured *http.Request and returns the raw *http.Response.
// The caller is responsible for closing the response body.
func (c *Client) Do(req *http.Request, opts ...RequestOption) (*http.Response, error) {
	return c.doRequest(req, opts...)
}
