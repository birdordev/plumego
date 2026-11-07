package gateway

import (
	"context"
	"net/http"
	"sync"
	"time"
)

// HealthChecker performs active health checks on backends
type HealthChecker struct {
	pool   *BackendPool
	config *HealthCheckConfig
	client *http.Client

	stopChan chan struct{}
	stopOnce sync.Once
	wg       sync.WaitGroup
}

// NewHealthChecker creates a new HealthChecker
func NewHealthChecker(pool *BackendPool, config *HealthCheckConfig) *HealthChecker {
	cfg := config.WithDefaults()

	return &HealthChecker{
		pool:   pool,
		config: cfg,
		client: &http.Client{
			Timeout: cfg.Timeout,
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				// Don't follow redirects for health checks
				return http.ErrUseLastResponse
			},
		},
		stopChan: make(chan struct{}),
	}
}

// Start starts the health checker
func (h *HealthChecker) Start() {
	h.wg.Add(1)
	go h.run()
}

// Stop stops the health checker
func (h *HealthChecker) Stop() {
	h.stopOnce.Do(func() {
		close(h.stopChan)
		h.wg.Wait()
	})
}

// run is the main health checking loop
func (h *HealthChecker) run() {
	defer h.wg.Done()

	ticker := time.NewTicker(h.config.Interval)
	defer ticker.Stop()

	// Perform initial health check
	h.checkAll()

	for {
		select {
		case <-ticker.C:
			h.checkAll()
		case <-h.stopChan:
			return
		}
	}
}

// checkAll checks all backends
func (h *HealthChecker) checkAll() {
	backends := h.pool.Backends()

	var wg sync.WaitGroup
	for _, backend := range backends {
		wg.Add(1)
		go func(b *Backend) {
			defer wg.Done()
			h.checkBackend(b)
		}(backend)
	}

	wg.Wait()
}

// checkBackend checks a single backend
func (h *HealthChecker) checkBackend(backend *Backend) {
	ctx, cancel := context.WithTimeout(context.Background(), h.config.Timeout)
	defer cancel()

	// Build health check URL
	healthURL := backend.URL + h.config.Path

	// Create request
	req, err := http.NewRequestWithContext(ctx, h.config.Method, healthURL, nil)
	if err != nil {
		h.updateHealth(backend, false)
		return
	}

	// Execute request
	resp, err := h.client.Do(req)
	if err != nil {
		h.updateHealth(backend, false)
		return
	}
	defer func() { _ = resp.Body.Close() }()

	// Check status code
	healthy := resp.StatusCode == h.config.ExpectedStatus
	h.updateHealth(backend, healthy)
}

// updateHealth updates the health status of a backend
func (h *HealthChecker) updateHealth(backend *Backend, healthy bool) {
	wasHealthy := backend.IsHealthy()

	// Update health status
	backend.SetHealthy(healthy)

	// Call health change callback if status changed
	if wasHealthy != healthy && h.config.OnHealthChange != nil {
		h.config.OnHealthChange(backend, healthy)
	}
}

// Check performs an immediate health check on a specific backend
func (h *HealthChecker) Check(backend *Backend) bool {
	h.checkBackend(backend)
	return backend.IsHealthy()
}

// CheckAll performs an immediate health check on all backends
func (h *HealthChecker) CheckAll() {
	h.checkAll()
}
