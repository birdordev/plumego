package rw

import (
	"context"
	"sync"
	"time"
)

// HealthChecker performs periodic health checks on replicas
type HealthChecker struct {
	config HealthCheckConfig

	// Track consecutive failures/successes for each replica
	failures  map[int]int
	successes map[int]int
	mu        sync.RWMutex

	lifecycleMu sync.Mutex
	cancel      context.CancelFunc
	stopped     bool
	wg          sync.WaitGroup
}

// NewHealthChecker creates a new health checker
func NewHealthChecker(config HealthCheckConfig) *HealthChecker {
	return &HealthChecker{
		config:    config,
		failures:  make(map[int]int),
		successes: make(map[int]int),
	}
}

// Start begins health checking in the background
func (hc *HealthChecker) Start(ctx context.Context, cluster *Cluster) {
	if !hc.config.Enabled {
		return
	}

	hc.lifecycleMu.Lock()
	defer hc.lifecycleMu.Unlock()
	if hc.stopped || hc.cancel != nil {
		return
	}
	ctx, hc.cancel = context.WithCancel(ctx)
	hc.wg.Add(1)
	go hc.run(ctx, cluster)
}

// Stop stops the health checker
func (hc *HealthChecker) Stop() {
	hc.lifecycleMu.Lock()
	hc.stopped = true
	if hc.cancel != nil {
		hc.cancel()
	}
	hc.lifecycleMu.Unlock()
	hc.wg.Wait()
}

// run is the main health check loop
func (hc *HealthChecker) run(ctx context.Context, cluster *Cluster) {
	defer hc.wg.Done()

	ticker := time.NewTicker(hc.config.Interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			hc.checkAll(ctx, cluster)
		}
	}
}

// checkAll checks health of all replicas
func (hc *HealthChecker) checkAll(ctx context.Context, cluster *Cluster) {
	replicas := cluster.Replicas()
	currentHealth := cluster.ReplicaHealth()

	for i, replica := range replicas {
		// Create timeout context for this check
		if ctx.Err() != nil {
			return
		}
		checkCtx, cancel := context.WithTimeout(ctx, hc.config.Timeout)

		err := replica.PingContext(checkCtx)
		cancel()

		cluster.metrics.HealthCheckCount.Add(1)
		if ctx.Err() != nil {
			return
		}

		// Update failure/success counters under hc.mu, then release the lock
		// before calling cluster.markReplicaHealth to avoid lock-order inversion
		// (hc.mu → cluster.mu would deadlock if another goroutine holds cluster.mu
		// and tries to acquire hc.mu).
		var shouldMark bool
		var markHealthy bool

		hc.mu.Lock()
		if err != nil {
			hc.failures[i]++
			hc.successes[i] = 0
			if currentHealth[i] && hc.failures[i] >= hc.config.FailureThreshold {
				shouldMark = true
				markHealthy = false
			}
		} else {
			hc.successes[i]++
			hc.failures[i] = 0
			if !currentHealth[i] && hc.successes[i] >= hc.config.RecoveryThreshold {
				shouldMark = true
				markHealthy = true
			}
		}
		hc.mu.Unlock()

		if shouldMark {
			cluster.markReplicaHealth(i, markHealthy)
		}
	}
}

// IsHealthy returns whether a replica is currently healthy
func (hc *HealthChecker) IsHealthy(idx int) bool {
	hc.mu.RLock()
	defer hc.mu.RUnlock()

	failures := hc.failures[idx]
	return failures < hc.config.FailureThreshold
}

// GetFailureCount returns the current failure count for a replica
func (hc *HealthChecker) GetFailureCount(idx int) int {
	hc.mu.RLock()
	defer hc.mu.RUnlock()
	return hc.failures[idx]
}

// GetSuccessCount returns the current success count for a replica
func (hc *HealthChecker) GetSuccessCount(idx int) int {
	hc.mu.RLock()
	defer hc.mu.RUnlock()
	return hc.successes[idx]
}

// Reset resets the health checker state
func (hc *HealthChecker) Reset() {
	hc.mu.Lock()
	defer hc.mu.Unlock()

	hc.failures = make(map[int]int)
	hc.successes = make(map[int]int)
}
