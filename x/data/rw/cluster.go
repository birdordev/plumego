package rw

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/birdordev/plumego/x/data/internal/testx"
)

var (
	// ErrNoPrimary is returned when primary database is not configured
	ErrNoPrimary = errors.New("rw: primary database not configured")

	// ErrAllReplicasDown is returned when all replicas are down and fallback is disabled
	ErrAllReplicasDown = errors.New("rw: all replicas are down")
)

// Config holds configuration for a read-write cluster
type Config struct {
	// Primary database connection
	Primary *sql.DB

	// Replica database connections
	Replicas []*sql.DB

	// Replica weights for weighted load balancing (optional)
	ReplicaWeights []int

	// LoadBalancer strategy (default: RoundRobin)
	LoadBalancer LoadBalancer

	// RoutingPolicy for determining primary vs replica routing (default: SQLTypePolicy)
	RoutingPolicy RoutingPolicy

	// HealthCheck configuration
	HealthCheck HealthCheckConfig

	// HealthCheckContext controls background health-check lifetime.
	// If nil, health checks use context.Background().
	HealthCheckContext context.Context

	// FallbackToPrimary sends reads to primary when all replicas are down.
	// The zero value is false so replica outages surface as routing errors.
	FallbackToPrimary bool
}

// HealthCheckConfig configures health checking behavior
type HealthCheckConfig struct {
	// Enabled turns health checking on/off
	Enabled bool

	// Interval between health checks
	Interval time.Duration

	// Timeout for each health check
	Timeout time.Duration

	// FailureThreshold number of consecutive failures before marking unhealthy
	FailureThreshold int

	// RecoveryThreshold number of consecutive successes before marking healthy
	RecoveryThreshold int
}

// DefaultHealthCheckConfig returns default health check configuration
func DefaultHealthCheckConfig() HealthCheckConfig {
	return HealthCheckConfig{
		Enabled:           true,
		Interval:          30 * time.Second,
		Timeout:           5 * time.Second,
		FailureThreshold:  3,
		RecoveryThreshold: 2,
	}
}

// Cluster represents a read-write database cluster
type Cluster struct {
	primary  *sql.DB
	replicas []*sql.DB

	lb            LoadBalancer
	policy        RoutingPolicy
	health        *HealthChecker
	healthConfig  HealthCheckConfig
	fallback      bool
	replicaHealth []bool
	mu            sync.RWMutex
	closeOnce     sync.Once
	closeErr      error

	// Metrics
	metrics ClusterMetrics
}

// ClusterMetrics holds cluster metrics
type ClusterMetrics struct {
	TotalQueries     atomic.Uint64
	PrimaryQueries   atomic.Uint64
	ReplicaQueries   atomic.Uint64
	RoutingErrors    atomic.Uint64
	HealthCheckCount atomic.Uint64
	FallbackCount    atomic.Uint64
}

// New creates a new read-write cluster
func New(config Config) (*Cluster, error) {
	if config.Primary == nil {
		return nil, ErrNoPrimary
	}
	replicas := append([]*sql.DB(nil), config.Replicas...)
	replicaWeights := append([]int(nil), config.ReplicaWeights...)
	if err := validateReplicaWeights(replicaWeights, len(replicas)); err != nil {
		return nil, err
	}

	// Set defaults
	if config.LoadBalancer == nil {
		if len(replicaWeights) > 0 {
			lb, err := NewWeightedBalancer(replicaWeights)
			if err != nil {
				return nil, err
			}
			config.LoadBalancer = lb
		} else {
			config.LoadBalancer = NewRoundRobinBalancer()
		}
	}

	if config.RoutingPolicy == nil {
		config.RoutingPolicy = NewTransactionAwarePolicy(NewSQLTypePolicy())
	}

	// Apply default health check values only if enabled and interval is 0
	if config.HealthCheck.Enabled && config.HealthCheck.Interval == 0 {
		defaults := DefaultHealthCheckConfig()
		config.HealthCheck.Interval = defaults.Interval
		config.HealthCheck.Timeout = defaults.Timeout
		if config.HealthCheck.FailureThreshold == 0 {
			config.HealthCheck.FailureThreshold = defaults.FailureThreshold
		}
		if config.HealthCheck.RecoveryThreshold == 0 {
			config.HealthCheck.RecoveryThreshold = defaults.RecoveryThreshold
		}
	}

	// Initialize cluster
	c := &Cluster{
		primary:       config.Primary,
		replicas:      replicas,
		lb:            config.LoadBalancer,
		policy:        config.RoutingPolicy,
		healthConfig:  config.HealthCheck,
		fallback:      config.FallbackToPrimary,
		replicaHealth: make([]bool, len(replicas)),
	}

	// Initially mark all replicas as healthy
	for i := range c.replicaHealth {
		c.replicaHealth[i] = true
	}

	// Start health checker if enabled
	if config.HealthCheck.Enabled && len(replicas) > 0 {
		c.health = NewHealthChecker(config.HealthCheck)
		healthCtx := config.HealthCheckContext
		if healthCtx == nil {
			healthCtx = context.Background()
		}
		c.health.Start(healthCtx, c)
	}

	return c, nil
}

// ExecContext executes a query that doesn't return rows (always uses primary)
func (c *Cluster) ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error) {
	c.metrics.TotalQueries.Add(1)
	c.metrics.PrimaryQueries.Add(1)

	return c.primary.ExecContext(ctx, query, args...)
}

// QueryContext executes a query that returns rows (may use replica)
func (c *Cluster) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	c.metrics.TotalQueries.Add(1)

	// Determine which database to use
	db, usedPrimary, err := c.selectDB(ctx, query)
	if err != nil {
		c.metrics.RoutingErrors.Add(1)
		return nil, err
	}

	if usedPrimary {
		c.metrics.PrimaryQueries.Add(1)
	} else {
		c.metrics.ReplicaQueries.Add(1)
	}

	return db.QueryContext(ctx, query, args...)
}

// QueryRowContext executes a query that returns at most one row (may use replica)
func (c *Cluster) QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row {
	c.metrics.TotalQueries.Add(1)

	// Determine which database to use
	db, usedPrimary, err := c.selectDB(ctx, query)
	if err != nil {
		c.metrics.RoutingErrors.Add(1)
		return queryRowError(err)
	}

	if usedPrimary {
		c.metrics.PrimaryQueries.Add(1)
	} else {
		c.metrics.ReplicaQueries.Add(1)
	}

	return db.QueryRowContext(ctx, query, args...)
}

func queryRowError(err error) *sql.Row {
	return testx.QueryRowError(err)
}

// BeginTx begins a transaction (always uses primary)
func (c *Cluster) BeginTx(ctx context.Context, opts *sql.TxOptions) (*sql.Tx, error) {
	c.metrics.TotalQueries.Add(1)
	c.metrics.PrimaryQueries.Add(1)

	// Mark context as being in transaction
	ctx = WithInTransaction(ctx)

	return c.primary.BeginTx(ctx, opts)
}

// PingContext verifies connections to all databases
func (c *Cluster) PingContext(ctx context.Context) error {
	// Ping primary
	if err := c.primary.PingContext(ctx); err != nil {
		return fmt.Errorf("primary ping failed: %w", err)
	}

	// Ping replicas
	for i, replica := range c.replicas {
		if err := replica.PingContext(ctx); err != nil {
			return fmt.Errorf("replica %d ping failed: %w", i, err)
		}
	}

	return nil
}

// Close stops the health checker and closes all database connections.
// It waits for the health checker goroutine to exit before closing connections
// to prevent pings against already-closed *sql.DB handles.
func (c *Cluster) Close() error {
	c.closeOnce.Do(func() {
		if c.health != nil {
			c.health.Stop()
		}

		var errs []error

		if err := c.primary.Close(); err != nil {
			errs = append(errs, fmt.Errorf("primary: %w", err))
		}

		for i, replica := range c.replicas {
			if err := replica.Close(); err != nil {
				errs = append(errs, fmt.Errorf("replica %d: %w", i, err))
			}
		}

		if len(errs) > 0 {
			c.closeErr = errs[0]
		}
	})
	return c.closeErr
}

func validateReplicaWeights(weights []int, replicaCount int) error {
	if len(weights) == 0 {
		return nil
	}
	if len(weights) != replicaCount {
		return fmt.Errorf("%w: got %d weights for %d replicas", ErrInvalidReplicaWeight, len(weights), replicaCount)
	}
	for i, weight := range weights {
		if weight <= 0 {
			return fmt.Errorf("%w: index %d has weight %d", ErrInvalidReplicaWeight, i, weight)
		}
	}
	return nil
}

// selectDB selects which database to use based on routing policy
// Returns (db, usedPrimary, error)
func (c *Cluster) selectDB(ctx context.Context, query string) (*sql.DB, bool, error) {
	// Check if we should use primary
	if c.policy.ShouldUsePrimary(ctx, query) {
		return c.primary, true, nil
	}

	// No replicas configured, use primary
	if len(c.replicas) == 0 {
		c.metrics.FallbackCount.Add(1)
		return c.primary, true, nil
	}

	// Select a replica using load balancer
	replicas := c.getReplicasForBalancer()
	idx, err := c.lb.Next(replicas)

	if err != nil {
		// All replicas are down
		if c.fallback {
			c.metrics.FallbackCount.Add(1)
			return c.primary, true, nil
		}
		return nil, false, err
	}

	return c.replicas[idx], false, nil
}

// getReplicasForBalancer returns replica info for the load balancer
func (c *Cluster) getReplicasForBalancer() []Replica {
	c.mu.RLock()
	defer c.mu.RUnlock()

	replicas := make([]Replica, len(c.replicas))
	for i, db := range c.replicas {
		replicas[i] = Replica{
			Index:     i,
			DB:        db,
			Weight:    1, // Default weight
			IsHealthy: c.replicaHealth[i],
		}
	}

	return replicas
}

// markReplicaHealth updates the health status of a replica
func (c *Cluster) markReplicaHealth(idx int, healthy bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if idx >= 0 && idx < len(c.replicaHealth) {
		c.replicaHealth[idx] = healthy
	}
}

// Metrics returns a pointer to cluster metrics
func (c *Cluster) Metrics() *ClusterMetrics {
	return &c.metrics
}

// Primary returns the primary database connection
func (c *Cluster) Primary() *sql.DB {
	return c.primary
}

// Replicas returns the replica database connections
func (c *Cluster) Replicas() []*sql.DB {
	return append([]*sql.DB(nil), c.replicas...)
}

// ReplicaHealth returns the health status of all replicas
func (c *Cluster) ReplicaHealth() []bool {
	c.mu.RLock()
	defer c.mu.RUnlock()

	health := make([]bool, len(c.replicaHealth))
	copy(health, c.replicaHealth)
	return health
}

// Stats returns database statistics
func (c *Cluster) Stats() ClusterStats {
	stats := ClusterStats{
		Primary:  c.primary.Stats(),
		Replicas: make([]sql.DBStats, len(c.replicas)),
	}

	for i, replica := range c.replicas {
		stats.Replicas[i] = replica.Stats()
	}

	return stats
}

// ClusterStats holds statistics for all databases in the cluster
type ClusterStats struct {
	Primary  sql.DBStats
	Replicas []sql.DBStats
}
