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
//	cfg := core.DefaultConfig()
//	app := core.New(cfg, core.AppDependencies{})
//
//	// Register proxy as a route handler
//	proxy := gateway.New(gateway.Config{
//		Targets: []string{
//			"http://backend-1:8080",
//			"http://backend-2:8080",
//		},
//	})
//	if err := app.Any("/api/*path", proxy); err != nil {
//		panic(err)
//	}
//
//	// Or register through AddRoute when wiring wants an explicit method.
//	if err := app.AddRoute(http.MethodGet, "/edge/*path", gateway.New(gateway.Config{
//		Targets: []string{"http://backend:8080"},
//	})); err != nil {
//		panic(err)
//	}
//
//	// Start with the explicit core lifecycle: Prepare + Server + Shutdown.
package gateway

import (
	"context"
	"io"
	"net/http"
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

	var pool *BackendPool
	var err error

	if len(cfg.Targets) > 0 {
		pool, err = NewBackendPool(cfg.Targets)
		if err != nil {
			return nil, err
		}
	} else {
		// Service discovery will populate backends dynamically
		pool = &BackendPool{backends: make([]*Backend, 0)}
	}

	transportPool := NewTransportPool(cfg.Transport)

	proxy := &Proxy{
		config:    cfg,
		pool:      pool,
		transport: transportPool,
		balancer:  cfg.LoadBalancer,
	}

	if lcb, ok := cfg.LoadBalancer.(*LeastConnectionsBalancer); ok {
		proxy.lcBalancer = lcb
	}

	if cfg.CircuitBreakerEnabled {
		for _, backend := range pool.Backends() {
			cb := newBackendCircuitBreaker(backend.URL, cfg.CircuitBreakerConfig)
			backend.SetCircuitBreaker(cb)
		}
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

	// Copy response body
	if resp.Body != nil {
		_, err = p.copyResponse(w, resp.Body)
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

// copyResponse copies the response body
func (p *Proxy) copyResponse(w io.Writer, src io.Reader) (int64, error) {
	if p.config.BufferPool != nil {
		// Use buffer pool
		buf := p.config.BufferPool.Get()
		defer p.config.BufferPool.Put(buf)
		return io.CopyBuffer(w, src, buf)
	}

	// Standard copy
	return io.Copy(w, src)
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
		err = p.handleWebSocketProxy(w, r, backend)

		if err == nil {
			// Success!
			backend.RecordSuccess()
			return
		}

		// Record failure
		backend.RecordFailure(p.config.FailureThreshold)
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

// watchServiceDiscovery watches for backend updates from service discovery.
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
			if len(backends) == 0 {
				continue
			}
			if err := p.pool.UpdateBackends(backends); err == nil {
				// Reset load balancer state on backend changes
				p.balancer.Reset()
			}
		}
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
