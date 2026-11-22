// Package gateway provides reverse proxy handlers for plumego
//
// This package implements a production-grade HTTP reverse proxy with features including:
//   - Load balancing (round-robin, weighted, least-connections, IP-hash)
//   - Health checking (active and passive)
//   - Service discovery integration
//   - Request/response modification
//   - Path rewriting
//   - Connection pooling and reuse
//   - Automatic retries
//   - WebSocket support
//
// Example usage:
//
//	import (
//		"net/http"
//
//		"github.com/birdordev/plumego/core"
//		"github.com/birdordev/plumego/x/gateway"
//	)
//
//	func registerProxy(app *core.App, backendURL string) (*gateway.Proxy, error) {
//		proxy, err := gateway.New(gateway.Config{Targets: []string{backendURL}})
//		if err != nil {
//			return nil, err
//		}
//		if err := app.AddRoute(http.MethodGet, "/edge/*path", proxy); err != nil {
//			proxy.Close()
//			return nil, err
//		}
//		return proxy, nil
//	}
//
// Close the returned proxy when the app no longer serves the registered route.
package gateway

import (
	"context"
	"errors"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// Proxy is the reverse proxy handler that implements http.Handler
type Proxy struct {
	config    *Config
	pool      *BackendPool
	transport *TransportPool
	balancer  LoadBalancer
	health    *HealthChecker

	discoveryCancel context.CancelFunc
	discoveryWG     sync.WaitGroup
	closeOnce       sync.Once
	copyBuffers     chan []byte

	// For least-connections balancer
	lcBalancer *LeastConnectionsBalancer
}

// New creates a new reverse proxy handler and returns an error for invalid
// configuration or backend pool initialization failures.
func New(config Config) (*Proxy, error) {
	cfg := config.WithDefaults()

	if err := cfg.Validate(); err != nil {
		return nil, err
	}

	// Discovery may create backends after New returns.
	if cfg.CircuitBreakerConfig != nil {
		breakerConfig := *cfg.CircuitBreakerConfig
		cfg.CircuitBreakerConfig = &breakerConfig
	}

	pool, err := NewBackendPool(cfg.Targets)
	if err != nil {
		return nil, err
	}

	transportPool := NewTransportPool(cfg.Transport)

	proxy := &Proxy{
		config:      cfg,
		pool:        pool,
		transport:   transportPool,
		balancer:    cfg.LoadBalancer,
		copyBuffers: make(chan []byte, 4),
	}

	if lcb, ok := cfg.LoadBalancer.(*LeastConnectionsBalancer); ok {
		proxy.lcBalancer = lcb
	}

	for _, backend := range pool.Backends() {
		proxy.configureBackend(backend)
	}

	if cfg.HealthCheck != nil {
		proxy.health = NewHealthChecker(pool, cfg.HealthCheck)
		proxy.health.Start()
	}

	if cfg.Discovery != nil {
		ctx, cancel := context.WithCancel(context.Background())
		proxy.discoveryCancel = cancel
		proxy.discoveryWG.Add(1)
		go proxy.watchServiceDiscovery(ctx)
	}

	return proxy, nil
}

// ServeHTTP implements http.Handler interface for the proxy.
// This allows the proxy to be used directly as a route handler.
func (p *Proxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Check if this is a WebSocket upgrade request
	if p.config.WebSocketEnabled && isWebSocketRequest(r) {
		p.handleWebSocket(w, r)
		return
	}

	// Handle HTTP request
	p.handleHTTP(w, r)
}

// Middleware returns a middleware function wrapper around the proxy.
// Use this if you need to use the proxy as middleware (rare).
// In most cases, you should use the proxy directly as a handler with app.Any() or similar.
func (p *Proxy) Middleware() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			p.ServeHTTP(w, r)
		})
	}
}

// handleHTTP handles HTTP requests
func (p *Proxy) handleHTTP(w http.ResponseWriter, r *http.Request) {
	var lastErr error
	attempt := 0

	// Retry loop
	for attempt <= p.config.RetryCount {
		// Select backend
		backend, err := p.balancer.Next(p.pool)
		if err != nil {
			p.config.ErrorHandler(w, r, err)
			return
		}

		// Track connection for least-connections balancer
		if p.lcBalancer != nil {
			p.lcBalancer.Acquire(backend)
			defer p.lcBalancer.Release(backend)
		}

		result := p.proxyRequest(w, r, backend)

		if result.err == nil {
			// Success!
			backend.RecordSuccess()
			return
		}
		if result.committed {
			return
		}

		// Record failure
		backend.RecordFailure(p.config.FailureThreshold)
		lastErr = NewProxyError(backend.URL, result.err, attempt)

		// Check if we should retry
		if attempt >= p.config.RetryCount {
			break
		}

		// Backoff before retry
		if p.config.RetryBackoff > 0 {
			timer := time.NewTimer(p.config.RetryBackoff * time.Duration(attempt+1))
			select {
			case <-r.Context().Done():
				timer.Stop()
				return
			case <-timer.C:
			}
		}

		attempt++
	}

	// All retries failed
	if lastErr != nil {
		p.config.ErrorHandler(w, r, lastErr)
	}
}

type proxyRequestResult struct {
	committed bool
	err       error
}

// proxyRequest proxies a single HTTP request to the backend
func (p *Proxy) proxyRequest(w http.ResponseWriter, r *http.Request, backend *Backend) proxyRequestResult {
	// Create a new request for the backend
	backendReq := p.createBackendRequest(r, backend)

	// Apply request modifications
	if err := p.applyRequestModifications(backendReq); err != nil {
		return proxyRequestResult{err: err}
	}

	// Get transport for this backend
	transport := p.transport.Get(backend.URL)
	client := &http.Client{
		Transport: transport,
		Timeout:   p.config.Timeout,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			// Don't follow redirects - return them to the client
			return http.ErrUseLastResponse
		},
	}

	// Execute request with circuit breaker protection
	var resp *http.Response
	err := backend.Execute(func() error {
		var execErr error
		resp, execErr = client.Do(backendReq)
		return execErr
	})

	if err != nil {
		return proxyRequestResult{err: err}
	}
	defer func() { _ = resp.Body.Close() }()

	// Apply response modifications
	if p.config.ModifyResponse != nil {
		if err := p.config.ModifyResponse(resp); err != nil {
			return proxyRequestResult{err: err}
		}
	}

	// Copy response headers
	for key, values := range resp.Header {
		for _, value := range values {
			w.Header().Add(key, value)
		}
	}

	// Write status code
	w.WriteHeader(resp.StatusCode)
	committed := true

	// Copy response body.
	if resp.Body != nil {
		var dst io.Writer = w
		if mediaType, _, _ := mime.ParseMediaType(resp.Header.Get("Content-Type")); mediaType == "text/event-stream" {
			flush := http.NewResponseController(w).Flush
			if err := flush(); err == nil {
				dst = eventStreamWriter{Writer: w, flush: flush}
			} else if !errors.Is(err, http.ErrNotSupported) {
				return proxyRequestResult{committed: committed, err: err}
			}
		}
		_, err = p.copyResponse(dst, resp.Body)
		if err != nil {
			return proxyRequestResult{committed: committed, err: err}
		}
	}

	return proxyRequestResult{committed: committed}
}

// createBackendRequest creates a new request for the backend
func (p *Proxy) createBackendRequest(r *http.Request, backend *Backend) *http.Request {
	// Clone the request
	backendReq := r.Clone(r.Context())

	// Set the backend URL
	backendReq.URL.Scheme = backend.ParsedURL.Scheme
	backendReq.URL.Host = backend.ParsedURL.Host

	// Clear RequestURI: http.Client.Do rejects requests with a non-empty RequestURI
	backendReq.RequestURI = ""

	// Apply path rewriting if configured
	if p.config.PathRewrite != nil {
		applyPathRewrite(backendReq, p.config.PathRewrite)
	}

	// Set or preserve host header
	if p.config.PreserveHost {
		backendReq.Host = r.Host
	} else {
		backendReq.Host = backend.ParsedURL.Host
	}

	return backendReq
}

// applyRequestModifications applies request modifications
func (p *Proxy) applyRequestModifications(r *http.Request) error {
	// Add forwarded headers
	if p.config.AddForwardedHeaders {
		if err := AddForwardedHeaders()(r); err != nil {
			return err
		}
	}

	// Remove hop-by-hop headers
	if p.config.RemoveHopByHop {
		if err := RemoveHopByHopHeaders()(r); err != nil {
			return err
		}
	}

	// Apply custom modifications
	if p.config.ModifyRequest != nil {
		if err := p.config.ModifyRequest(r); err != nil {
			return err
		}
	}

	return nil
}

type eventStreamWriter struct {
	io.Writer
	flush func() error
}

func (w eventStreamWriter) Write(p []byte) (int, error) {
	n, err := w.Writer.Write(p)
	if n > 0 && err == nil {
		err = w.flush()
	}
	return n, err
}

// copyResponse copies the response body
func (p *Proxy) copyResponse(w io.Writer, src io.Reader) (int64, error) {
	if p.config.BufferPool != nil {
		// Use buffer pool
		buf := p.config.BufferPool.Get()
		defer p.config.BufferPool.Put(buf)
		return io.CopyBuffer(w, src, buf)
	}

	// Prefer the same specialized interfaces as io.Copy.
	if _, ok := src.(io.WriterTo); ok {
		return io.Copy(w, src)
	}
	if _, ok := w.(io.ReaderFrom); ok {
		return io.Copy(w, src)
	}

	var buf []byte
	select {
	case buf = <-p.copyBuffers:
	default:
		buf = make([]byte, 32<<10)
	}
	defer func() {
		select {
		case p.copyBuffers <- buf:
		default:
		}
	}()
	return io.CopyBuffer(w, src, buf)
}

// isWebSocketRequest checks if the request is a WebSocket upgrade request
func isWebSocketRequest(r *http.Request) bool {
	return strings.ToLower(r.Header.Get("Connection")) == "upgrade" &&
		strings.ToLower(r.Header.Get("Upgrade")) == "websocket"
}

// handleWebSocket handles WebSocket proxying
func (p *Proxy) handleWebSocket(w http.ResponseWriter, r *http.Request) {
	var lastErr error
	attempt := 0

	// Retry loop
	for attempt <= p.config.RetryCount {
		// Select backend
		backend, err := p.balancer.Next(p.pool)
		if err != nil {
			p.config.ErrorHandler(w, r, err)
			return
		}

		// Proxy WebSocket connection
		hijacked, err := p.handleWebSocketProxy(w, r, backend)

		if err == nil {
			// Success!
			backend.RecordSuccess()
			return
		}

		// Record failure
		backend.RecordFailure(p.config.FailureThreshold)
		if hijacked {
			return
		}
		lastErr = NewProxyError(backend.URL, err, attempt)

		// Check if we should retry
		if attempt >= p.config.RetryCount {
			break
		}

		// Backoff before retry
		if p.config.RetryBackoff > 0 {
			timer := time.NewTimer(p.config.RetryBackoff * time.Duration(attempt+1))
			select {
			case <-r.Context().Done():
				timer.Stop()
				return
			case <-timer.C:
			}
		}

		attempt++
	}

	// All retries failed
	if lastErr != nil {
		p.config.ErrorHandler(w, r, lastErr)
	}
}

// watchServiceDiscovery replaces the backend pool with each discovery snapshot.
// An empty snapshot removes all backends.
func (p *Proxy) watchServiceDiscovery(ctx context.Context) {
	defer p.discoveryWG.Done()

	if p.config.Discovery == nil {
		return
	}

	updateChan, err := p.config.Discovery.Watch(ctx, p.config.ServiceName)
	if err != nil {
		return
	}
	if updateChan == nil {
		return
	}

	for {
		select {
		case <-ctx.Done():
			return
		case backends, ok := <-updateChan:
			if !ok {
				return
			}
			p.applyDiscoverySnapshot(backends)
		}
	}
}

func (p *Proxy) configureBackend(backend *Backend) {
	if p.config.CircuitBreakerEnabled {
		backend.SetCircuitBreaker(newBackendCircuitBreaker(backend.URL, p.config.CircuitBreakerConfig))
	}
}

// applyDiscoverySnapshot is called by the single discovery watcher.
func (p *Proxy) applyDiscoverySnapshot(urls []string) {
	current := p.pool.Backends()
	byURL := make(map[string][]*Backend, len(current))
	for _, backend := range current {
		key := backend.ParsedURL.String()
		byURL[key] = append(byURL[key], backend)
	}

	next := make([]*Backend, 0, len(urls))
	membershipChanged := len(current) != len(urls)
	for _, target := range urls {
		parsed, err := url.Parse(target)
		if err != nil || parsed.Hostname() == "" {
			return
		}
		switch parsed.Scheme {
		case "http", "https", "ws", "wss":
		default:
			return
		}

		key := parsed.String()
		if existing := byURL[key]; len(existing) > 0 {
			next = append(next, existing[0])
			byURL[key] = existing[1:]
			continue
		}

		backend := newBackend(target, parsed)
		p.configureBackend(backend)
		next = append(next, backend)
		membershipChanged = true
	}

	p.pool.replaceBackends(next)
	if membershipChanged {
		p.balancer.Reset()
	}
}

// Close closes the proxy and cleans up resources
func (p *Proxy) Close() {
	p.closeOnce.Do(func() {
		if p.discoveryCancel != nil {
			p.discoveryCancel()
			p.discoveryWG.Wait()
		}

		// Stop health checker
		if p.health != nil {
			p.health.Stop()
		}

		// Close transport pool
		p.transport.Close()
	})
}

// Stats returns proxy statistics
func (p *Proxy) Stats() ProxyStats {
	return ProxyStats{
		TotalBackends:   p.pool.Count(),
		HealthyBackends: p.pool.HealthyCount(),
		Strategy:        p.balancer.Name(),
	}
}

// ProxyStats holds proxy statistics
type ProxyStats struct {
	TotalBackends   int
	HealthyBackends int
	Strategy        string
}
