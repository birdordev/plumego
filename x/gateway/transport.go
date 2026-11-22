package gateway

import (
	"crypto/tls"
	"net"
	"net/http"
	"slices"
	"sync"
	"time"
)

// TransportConfig holds HTTP transport configuration
type TransportConfig struct {
	// MaxIdleConns controls the maximum number of idle connections
	// Default: 100
	MaxIdleConns int

	// MaxIdleConnsPerHost controls the maximum idle connections per host
	// Default: 10
	MaxIdleConnsPerHost int

	// IdleConnTimeout is the maximum time an idle connection is kept alive
	// Default: 90 seconds
	IdleConnTimeout time.Duration

	// ResponseHeaderTimeout specifies the amount of time to wait for a
	// server's response headers
	// Default: 30 seconds
	ResponseHeaderTimeout time.Duration

	// TLSHandshakeTimeout specifies the maximum amount of time waiting to
	// wait for a TLS handshake
	// Default: 10 seconds
	TLSHandshakeTimeout time.Duration

	// ExpectContinueTimeout specifies the amount of time to wait for a
	// server's first response headers after fully writing the request
	// Default: 1 second
	ExpectContinueTimeout time.Duration

	// DisableKeepAlives disables HTTP keep-alives
	// Default: false (keep-alives enabled)
	DisableKeepAlives bool

	// DisableCompression disables compression
	// Default: false (compression enabled)
	DisableCompression bool

	// MaxConnsPerHost limits the total number of connections per host
	// Default: 0 (unlimited)
	MaxConnsPerHost int

	// DialTimeout is the maximum amount of time a dial will wait for
	// a connect to complete
	// Default: 30 seconds
	DialTimeout time.Duration

	// DialKeepAlive specifies the interval between keep-alive probes
	// Default: 30 seconds
	DialKeepAlive time.Duration

	// TLSClientConfig specifies the TLS configuration to use
	// Default: nil (uses default TLS config)
	TLSClientConfig *tls.Config
}

// DefaultTransportConfig returns a TransportConfig with default values
func DefaultTransportConfig() *TransportConfig {
	return &TransportConfig{
		MaxIdleConns:          100,
		MaxIdleConnsPerHost:   10,
		IdleConnTimeout:       90 * time.Second,
		ResponseHeaderTimeout: 30 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
		DisableKeepAlives:     false,
		DisableCompression:    false,
		MaxConnsPerHost:       0,
		DialTimeout:           30 * time.Second,
		DialKeepAlive:         30 * time.Second,
	}
}

// TransportPool manages HTTP transports for multiple backends
type TransportPool struct {
	transports map[string]*http.Transport
	config     *TransportConfig
	mu         sync.RWMutex
}

// NewTransportPool creates a pool from a snapshot of config. TLS values
// referenced by the copied tls.Config follow the standard library's ownership
// rules and must remain immutable.
func NewTransportPool(config *TransportConfig) *TransportPool {
	if config == nil {
		config = DefaultTransportConfig()
	} else {
		copy := *config
		copy.TLSClientConfig = config.TLSClientConfig.Clone()
		config = &copy
	}

	return &TransportPool{
		transports: make(map[string]*http.Transport),
		config:     config,
	}
}

// Get returns a transport for the given backend URL
// If a transport doesn't exist, it creates a new one
func (p *TransportPool) Get(backendURL string) *http.Transport {
	p.mu.RLock()
	transport, exists := p.transports[backendURL]
	p.mu.RUnlock()

	if exists {
		return transport
	}

	// Create new transport
	p.mu.Lock()
	defer p.mu.Unlock()

	// Double-check after acquiring write lock
	if transport, exists := p.transports[backendURL]; exists {
		return transport
	}

	transport = p.createTransport()
	p.transports[backendURL] = transport

	return transport
}

// createTransport creates a new HTTP transport with the configured settings
func (p *TransportPool) createTransport() *http.Transport {
	config := p.config.TLSClientConfig.Clone()
	http2 := config == nil || len(config.NextProtos) == 0 || slices.Contains(config.NextProtos, "h2")
	return &http.Transport{
		Proxy: http.ProxyFromEnvironment,
		DialContext: (&net.Dialer{
			Timeout:   p.config.DialTimeout,
			KeepAlive: p.config.DialKeepAlive,
		}).DialContext,
		MaxIdleConns:          p.config.MaxIdleConns,
		MaxIdleConnsPerHost:   p.config.MaxIdleConnsPerHost,
		MaxConnsPerHost:       p.config.MaxConnsPerHost,
		IdleConnTimeout:       p.config.IdleConnTimeout,
		TLSHandshakeTimeout:   p.config.TLSHandshakeTimeout,
		ExpectContinueTimeout: p.config.ExpectContinueTimeout,
		ResponseHeaderTimeout: p.config.ResponseHeaderTimeout,
		DisableKeepAlives:     p.config.DisableKeepAlives,
		DisableCompression:    p.config.DisableCompression,
		TLSClientConfig:       config,
		ForceAttemptHTTP2:     http2,
	}
}

// Close closes all transports and cleans up idle connections
func (p *TransportPool) Close() {
	p.mu.Lock()
	defer p.mu.Unlock()

	for _, transport := range p.transports {
		transport.CloseIdleConnections()
	}

	p.transports = make(map[string]*http.Transport)
}

// CloseIdleConnections closes idle connections for all transports
func (p *TransportPool) CloseIdleConnections() {
	p.mu.RLock()
	defer p.mu.RUnlock()

	for _, transport := range p.transports {
		transport.CloseIdleConnections()
	}
}

// Remove removes a transport from the pool
func (p *TransportPool) Remove(backendURL string) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if transport, exists := p.transports[backendURL]; exists {
		transport.CloseIdleConnections()
		delete(p.transports, backendURL)
	}
}

// Count returns the number of transports in the pool
func (p *TransportPool) Count() int {
	p.mu.RLock()
	defer p.mu.RUnlock()

	return len(p.transports)
}
