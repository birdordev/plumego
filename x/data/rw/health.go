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
	health    map[int]bool
	mu        sync.RWMutex

	lifecycleMu sync.Mutex
	cancel      context.CancelFunc
	stopped     bool
	wg          sync.WaitGroup
}

// NewHealthChecker creates a health checker. A nonpositive interval uses
// the default interval.
func NewHealthChecker(config HealthCheckConfig) *HealthChecker {
	if config.Interval <= 0 {
		config.Interval = DefaultHealthCheckConfig().Interval
	}
	return &HealthChecker{
		config:    config,
		failures:  make(map[int]int),
		successes: make(map[int]int),
		health:    make(map[int]bool),
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
		healthy := currentHealth[i]
		hc.mu.Lock()
		if err != nil {
			hc.failures[i]++
			hc.successes[i] = 0
			if hc.failures[i] >= hc.config.FailureThreshold {
				healthy = false
			}
		} else {
			hc.successes[i]++
			hc.failures[i] = 0
			if hc.successes[i] >= hc.config.RecoveryThreshold {
				healthy = true
			}
		}
		hc.health[i] = healthy
		hc.mu.Unlock()

		if healthy != currentHealth[i] {
			cluster.markReplicaHealth(i, healthy)
		}
	}
}

// IsHealthy reports the last replica state after applying the failure and
// recovery thresholds. Reset clears the observed states.
func (hc *HealthChecker) IsHealthy(idx int) bool {
	hc.mu.RLock()
	defer hc.mu.RUnlock()

	if healthy, observed := hc.health[idx]; observed {
		return healthy
	}
	return 0 < hc.config.FailureThreshold
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
	hc.health = make(map[int]bool)
}
