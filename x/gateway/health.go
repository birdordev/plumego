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

	mu      sync.Mutex
	started bool
	ctx     context.Context
	cancel  context.CancelFunc
	wg      sync.WaitGroup
}

// NewHealthChecker creates a new HealthChecker. A non-positive interval uses
// the default interval of 10 seconds.
func NewHealthChecker(pool *BackendPool, config *HealthCheckConfig) *HealthChecker {
	cfg := config.WithDefaults()
	ctx, cancel := context.WithCancel(context.Background())

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
		ctx:    ctx,
		cancel: cancel,
	}
}

// Start starts background checks once. After Stop, Start is a no-op.
func (h *HealthChecker) Start() {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.started || h.ctx.Err() != nil {
		return
	}
	h.started = true
	h.wg.Add(1)
	go h.run()
}

// Stop cancels and waits for background checks, including in-flight probes.
// Explicit Check and CheckAll calls remain available after Stop.
func (h *HealthChecker) Stop() {
	h.mu.Lock()
	h.cancel()
	h.mu.Unlock()
	h.wg.Wait()
}

// run is the main health checking loop
func (h *HealthChecker) run() {
	defer h.wg.Done()

	ticker := time.NewTicker(h.config.Interval)
	defer ticker.Stop()

	// Perform initial health check
	h.checkAll(h.ctx)

	for {
		select {
		case <-ticker.C:
			h.checkAll(h.ctx)
		case <-h.ctx.Done():
			return
		}
	}
}

// checkAll checks all backends
func (h *HealthChecker) checkAll(ctx context.Context) {
	if ctx.Err() != nil {
		return
	}
	backends := h.pool.Backends()

	var wg sync.WaitGroup
	for _, backend := range backends {
		wg.Add(1)
		go func(b *Backend) {
			defer wg.Done()
			h.checkBackend(ctx, b)
		}(backend)
	}

	wg.Wait()
}

// checkBackend checks a single backend
func (h *HealthChecker) checkBackend(parent context.Context, backend *Backend) {
	sequence := backend.probeSequence.Add(1)
	ctx, cancel := context.WithTimeout(parent, h.config.Timeout)
	defer cancel()

	// Build health check URL
	healthURL := backend.URL + h.config.Path

	// Create request
	req, err := http.NewRequestWithContext(ctx, h.config.Method, healthURL, nil)
	if err != nil {
		h.updateHealth(parent, backend, sequence, false)
		return
	}

	// Execute request
	resp, err := h.client.Do(req)
	if err != nil {
		h.updateHealth(parent, backend, sequence, false)
		return
	}
	defer func() { _ = resp.Body.Close() }()

	// Check status code
	healthy := resp.StatusCode == h.config.ExpectedStatus
	h.updateHealth(parent, backend, sequence, healthy)
}

// updateHealth updates the health status of a backend
func (h *HealthChecker) updateHealth(ctx context.Context, backend *Backend, sequence uint64, healthy bool) {
	// Serialize publication with background cancellation. A per-probe
	// deadline is still an unhealthy result if the parent remains active.
	h.mu.Lock()
	backend.probeMu.Lock()
	if ctx.Err() != nil || sequence <= backend.appliedProbe {
		backend.probeMu.Unlock()
		h.mu.Unlock()
		return
	}
	wasHealthy := backend.IsHealthy()

	backend.appliedProbe = sequence
	backend.SetHealthy(healthy)
	backend.probeMu.Unlock()
	h.mu.Unlock()

	// Call health change callback if status changed
	if wasHealthy != healthy && h.config.OnHealthChange != nil {
		h.config.OnHealthChange(backend, healthy)
	}
}

// Check performs an immediate health check on a specific backend and returns
// its current health. An overlapping check started earlier cannot overwrite
// a result already applied by a newer check.
func (h *HealthChecker) Check(backend *Backend) bool {
	h.checkBackend(context.Background(), backend)
	return backend.IsHealthy()
}

// CheckAll performs an immediate health check on all backends
func (h *HealthChecker) CheckAll() {
	h.checkAll(context.Background())
}
