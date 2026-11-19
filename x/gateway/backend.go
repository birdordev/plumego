package gateway

import (
	"net/url"
	"sync"
	"sync/atomic"
)

// Backend represents a backend server
type Backend struct {
	// URL is the backend URL (e.g., "http://localhost:8080")
	URL string

	// ParsedURL is the parsed URL
	ParsedURL *url.URL

	// Weight is the backend weight for load balancing
	// Higher weight = more traffic
	// Default: 1
	Weight int

	// Metadata contains additional backend information
	Metadata map[string]string

	// Health status
	healthy       atomic.Bool
	probeSequence atomic.Uint64
	probeMu       sync.Mutex
	appliedProbe  uint64 // Guarded by probeMu.

	// Failure tracking
	consecutiveFailures atomic.Uint32
	totalRequests       atomic.Uint64
	totalFailures       atomic.Uint64
	totalSuccesses      atomic.Uint64

	// Circuit breaker (optional)
	circuitBreaker CircuitBreaker
}

// NewBackend creates a new backend
func NewBackend(urlStr string) (*Backend, error) {
	parsedURL, err := url.Parse(urlStr)
	if err != nil {
		return nil, err
	}

	backend := &Backend{
		URL:       urlStr,
		ParsedURL: parsedURL,
		Weight:    1,
		Metadata:  make(map[string]string),
	}

	// Mark as healthy by default
	backend.healthy.Store(true)

	return backend, nil
}

// IsHealthy returns whether the backend is healthy
func (b *Backend) IsHealthy() bool {
	return b.healthy.Load()
}

// SetHealthy sets the backend health status
func (b *Backend) SetHealthy(healthy bool) {
	b.healthy.Store(healthy)

	// Reset consecutive failures on recovery
	if healthy {
		b.consecutiveFailures.Store(0)
	}
}

// SetCircuitBreaker sets the circuit breaker for this backend
func (b *Backend) SetCircuitBreaker(cb CircuitBreaker) {
	b.circuitBreaker = cb
}

// HasCircuitBreaker returns whether this backend has a circuit breaker
func (b *Backend) HasCircuitBreaker() bool {
	return b.circuitBreaker != nil
}

// Execute executes a function with circuit breaker protection
// If no circuit breaker is set, executes directly
func (b *Backend) Execute(fn func() error) error {
	if b.circuitBreaker == nil {
		return fn()
	}

	return b.circuitBreaker.Call(fn)
}

// RecordSuccess records a successful request
func (b *Backend) RecordSuccess() {
	b.totalRequests.Add(1)
	b.totalSuccesses.Add(1)
	b.consecutiveFailures.Store(0)

	// Auto-recover after successful request
	b.SetHealthy(true)
}

// RecordFailure records a failed request
func (b *Backend) RecordFailure(threshold uint32) {
	b.totalRequests.Add(1)
	b.totalFailures.Add(1)

	failures := b.consecutiveFailures.Add(1)

	// Mark as unhealthy if threshold exceeded
	if failures >= threshold {
		b.SetHealthy(false)
	}
}

// Stats returns backend statistics
func (b *Backend) Stats() BackendStats {
	return BackendStats{
		URL:                 b.URL,
		Healthy:             b.IsHealthy(),
		Weight:              b.Weight,
		TotalRequests:       b.totalRequests.Load(),
		TotalFailures:       b.totalFailures.Load(),
		TotalSuccesses:      b.totalSuccesses.Load(),
		ConsecutiveFailures: b.consecutiveFailures.Load(),
	}
}

// BackendStats holds backend statistics
type BackendStats struct {
	URL                 string
	Healthy             bool
	Weight              int
	TotalRequests       uint64
	TotalFailures       uint64
	TotalSuccesses      uint64
	ConsecutiveFailures uint32
}

// BackendPool manages a pool of backends
type BackendPool struct {
	backends []*Backend
	mu       sync.RWMutex
}

// NewBackendPool creates a new backend pool
func NewBackendPool(urls []string) (*BackendPool, error) {
	backends := make([]*Backend, 0, len(urls))

	for _, urlStr := range urls {
		backend, err := NewBackend(urlStr)
		if err != nil {
			return nil, err
		}
		backends = append(backends, backend)
	}

	return &BackendPool{
		backends: backends,
	}, nil
}

// Backends returns all backends
func (p *BackendPool) Backends() []*Backend {
	p.mu.RLock()
	defer p.mu.RUnlock()

	result := make([]*Backend, len(p.backends))
	copy(result, p.backends)

	return result
}

// HealthyBackends returns only healthy backends
func (p *BackendPool) HealthyBackends() []*Backend {
	p.mu.RLock()
	defer p.mu.RUnlock()

	result := make([]*Backend, 0, len(p.backends))
	for _, backend := range p.backends {
		if backend.IsHealthy() {
			result = append(result, backend)
		}
	}

	return result
}

// Count returns the total number of backends
func (p *BackendPool) Count() int {
	p.mu.RLock()
	defer p.mu.RUnlock()

	return len(p.backends)
}

// HealthyCount returns the number of healthy backends
func (p *BackendPool) HealthyCount() int {
	p.mu.RLock()
	defer p.mu.RUnlock()

	count := 0
	for _, backend := range p.backends {
		if backend.IsHealthy() {
			count++
		}
	}

	return count
}

// AddBackend adds a backend to the pool
func (p *BackendPool) AddBackend(backend *Backend) {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.backends = append(p.backends, backend)
}

// RemoveBackend removes a backend from the pool
func (p *BackendPool) RemoveBackend(url string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()

	for i, backend := range p.backends {
		if backend.URL == url {
			p.backends = append(p.backends[:i], p.backends[i+1:]...)
			return true
		}
	}

	return false
}

// UpdateBackends updates the pool with new backends
func (p *BackendPool) UpdateBackends(urls []string) error {
	newBackends := make([]*Backend, 0, len(urls))

	for _, urlStr := range urls {
		backend, err := NewBackend(urlStr)
		if err != nil {
			return err
		}
		newBackends = append(newBackends, backend)
	}

	p.mu.Lock()
	p.backends = newBackends
	p.mu.Unlock()

	return nil
}

// GetBackend returns a backend by URL
func (p *BackendPool) GetBackend(url string) *Backend {
	p.mu.RLock()
	defer p.mu.RUnlock()

	for _, backend := range p.backends {
		if backend.URL == url {
			return backend
		}
	}

	return nil
}

// Stats returns statistics for all backends
func (p *BackendPool) Stats() []BackendStats {
	p.mu.RLock()
	defer p.mu.RUnlock()

	stats := make([]BackendStats, len(p.backends))
	for i, backend := range p.backends {
		stats[i] = backend.Stats()
	}

	return stats
}
