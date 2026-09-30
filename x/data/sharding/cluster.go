package sharding

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/spcent/plumego/x/data/rw"
)

// ClusterDB is a convenience wrapper that builds one rw.Cluster per shard from
// configuration and exposes the Router-facing database methods. Callers that
// already own rw.Cluster instances should prefer NewRouter directly. New takes
// ownership of DB handles supplied in ClusterConfig and closes them from Close.
type ClusterDB struct {
	router *Router
	config ClusterConfig
}

// ClusterConfig holds the configuration for a sharded cluster
type ClusterConfig struct {
	// Shards defines the configuration for each shard
	Shards []ShardConfig

	// ShardingRules defines the sharding rules for tables
	ShardingRules []ShardingRuleConfig

	// CrossShardPolicy determines how to handle cross-shard queries
	CrossShardPolicy CrossShardPolicy

	// DefaultShardIndex is used when no shard can be determined (-1 to disable)
	DefaultShardIndex int

	// EnableMetrics enables query metrics collection
	EnableMetrics bool
}

// ShardConfig defines the configuration for a single shard
type ShardConfig struct {
	// Primary database connection. New takes ownership and closes it from Close.
	Primary *sql.DB

	// Replicas database connections (optional for read-write splitting).
	// New takes ownership and closes them from Close.
	Replicas []*sql.DB

	// ReplicaWeights for weighted load balancing (optional)
	ReplicaWeights []int

	// HealthCheck configuration for replicas
	HealthCheck rw.HealthCheckConfig

	// FallbackToPrimary routes reads to primary when all replicas are down.
	// Keep false unless primary-backed outage reads are an explicit service policy.
	FallbackToPrimary bool
}

// ShardingRuleConfig defines the configuration for a sharding rule
type ShardingRuleConfig struct {
	// TableName is the logical table name
	TableName string

	// ShardKeyColumn is the column name used for sharding
	ShardKeyColumn string

	// Strategy is the sharding strategy name ("hash", "mod", "range", "list")
	Strategy string

	// StrategyConfig is strategy-specific configuration
	StrategyConfig StrategyConfig

	// ActualTableNames maps shard index to physical table name (optional)
	ActualTableNames map[int]string

	// DefaultShard specifies which shard to use when shard key is not present (-1 to disable)
	DefaultShard int
}

// StrategyConfig holds strategy-specific configuration
type StrategyConfig struct {
	// For range strategy: range definitions
	Ranges []RangeDefinition

	// For list strategy: value to shard mapping
	Mapping map[string]int

	// For list strategy: default shard when value not in mapping
	DefaultShard int
}

// DefaultClusterConfig returns a default cluster configuration
func DefaultClusterConfig() ClusterConfig {
	return ClusterConfig{
		Shards:            []ShardConfig{},
		ShardingRules:     []ShardingRuleConfig{},
		CrossShardPolicy:  CrossShardDeny,
		DefaultShardIndex: -1,
		EnableMetrics:     true,
	}
}

// DefaultShardConfig returns a default shard configuration
func DefaultShardConfig() ShardConfig {
	return ShardConfig{
		Replicas:          []*sql.DB{},
		ReplicaWeights:    []int{},
		HealthCheck:       rw.DefaultHealthCheckConfig(),
		FallbackToPrimary: false,
	}
}

// Validate validates the cluster configuration
func (c ClusterConfig) Validate() error {
	if len(c.Shards) == 0 {
		return fmt.Errorf("at least one shard is required")
	}

	// Validate each shard configuration
	for i, shard := range c.Shards {
		if shard.Primary == nil {
			return fmt.Errorf("shard %d primary database is required", i)
		}

		for j, replica := range shard.Replicas {
			if replica == nil {
				return fmt.Errorf("shard %d replica %d database is required", i, j)
			}
		}

		// Validate replica weights
		if len(shard.ReplicaWeights) > 0 && len(shard.ReplicaWeights) != len(shard.Replicas) {
			return fmt.Errorf("shard %d: replica weights count must match replicas count", i)
		}
	}

	// Validate sharding rules
	if len(c.ShardingRules) == 0 {
		return fmt.Errorf("at least one sharding rule is required")
	}

	for i, rule := range c.ShardingRules {
		if rule.TableName == "" {
			return fmt.Errorf("sharding rule %d: table name is required", i)
		}
		if rule.ShardKeyColumn == "" {
			return fmt.Errorf("sharding rule %d: shard key column is required", i)
		}
		if rule.Strategy == "" {
			return fmt.Errorf("sharding rule %d: strategy is required", i)
		}
	}

	// Validate default shard index
	if c.DefaultShardIndex < -1 {
		return fmt.Errorf("default shard index %d must be -1 or greater", c.DefaultShardIndex)
	}
	if c.DefaultShardIndex >= len(c.Shards) {
		return fmt.Errorf("default shard index %d exceeds shard count %d", c.DefaultShardIndex, len(c.Shards))
	}

	return nil
}

// New creates a new sharded cluster database
func New(config ClusterConfig) (*ClusterDB, error) {
	// Validate configuration
	if err := config.Validate(); err != nil {
		return nil, fmt.Errorf("invalid cluster config: %w", err)
	}

	// Create shards
	shards := make([]*rw.Cluster, len(config.Shards))
	for i, shardConfig := range config.Shards {
		shard, err := createShard(shardConfig)
		if err != nil {
			// Close previously created shards on error
			for j := 0; j < i; j++ {
				_ = shards[j].Close()
			}
			return nil, fmt.Errorf("failed to create shard %d: %w", i, err)
		}
		shards[i] = shard
	}

	// Create sharding rule registry
	registry := NewShardingRuleRegistry()
	for _, ruleConfig := range config.ShardingRules {
		strategy, err := createStrategy(ruleConfig.Strategy, ruleConfig.StrategyConfig)
		if err != nil {
			// Close shards on error
			for _, shard := range shards {
				_ = shard.Close()
			}
			return nil, fmt.Errorf("failed to create strategy for table %s: %w", ruleConfig.TableName, err)
		}

		rule, err := NewShardingRule(ruleConfig.TableName, ruleConfig.ShardKeyColumn, strategy, len(shards))
		if err != nil {
			// Close shards on error
			for _, shard := range shards {
				_ = shard.Close()
			}
			return nil, fmt.Errorf("failed to create sharding rule for table %s: %w", ruleConfig.TableName, err)
		}

		// Set actual table names if provided
		if len(ruleConfig.ActualTableNames) > 0 {
			for idx, name := range ruleConfig.ActualTableNames {
				rule.SetActualTableName(idx, name)
			}
		}

		// Set default shard if provided
		if ruleConfig.DefaultShard >= 0 {
			if err := rule.SetDefaultShard(ruleConfig.DefaultShard); err != nil {
				// Close shards on error
				for _, shard := range shards {
					_ = shard.Close()
				}
				return nil, fmt.Errorf("failed to set default shard for table %s: %w", ruleConfig.TableName, err)
			}
		}

		if err := registry.Register(rule); err != nil {
			// Close shards on error
			for _, shard := range shards {
				_ = shard.Close()
			}
			return nil, fmt.Errorf("failed to register sharding rule for table %s: %w", ruleConfig.TableName, err)
		}
	}

	// Create router
	routerOpts := []RouterOption{
		WithCrossShardPolicy(config.CrossShardPolicy),
		WithMetrics(config.EnableMetrics),
	}
	if config.DefaultShardIndex >= 0 {
		routerOpts = append(routerOpts, WithDefaultShard(config.DefaultShardIndex))
	}

	router, err := NewRouter(shards, registry, routerOpts...)
	if err != nil {
		// Close shards on error
		for _, shard := range shards {
			_ = shard.Close()
		}
		return nil, fmt.Errorf("failed to create router: %w", err)
	}

	return &ClusterDB{
		router: router,
		config: config,
	}, nil
}

// createShard creates a read-write cluster shard from configuration
func createShard(config ShardConfig) (*rw.Cluster, error) {
	rwConfig := rw.Config{
		Primary:           config.Primary,
		Replicas:          config.Replicas,
		ReplicaWeights:    config.ReplicaWeights,
		HealthCheck:       config.HealthCheck,
		FallbackToPrimary: config.FallbackToPrimary,
	}

	cluster, err := rw.New(rwConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create read-write cluster: %w", err)
	}

	return cluster, nil
}

// createStrategy creates a sharding strategy from configuration
func createStrategy(strategyName string, config StrategyConfig) (Strategy, error) {
	switch strategyName {
	case "hash":
		return NewHashStrategy(), nil
	case "mod":
		return NewModStrategy(), nil
	case "range":
		if len(config.Ranges) == 0 {
			return nil, fmt.Errorf("range strategy requires range definitions")
		}
		return NewRangeStrategy(config.Ranges)
	case "list":
		var mapping map[any]int
		if len(config.Mapping) > 0 {
			mapping = make(map[any]int, len(config.Mapping))
			for key, shard := range config.Mapping {
				mapping[key] = shard
			}
		} else {
			mapping = make(map[any]int)
		}

		if config.DefaultShard >= 0 {
			return NewListStrategyWithDefault(mapping, config.DefaultShard), nil
		}
		return NewListStrategy(mapping), nil
	default:
		return nil, fmt.Errorf("unknown strategy: %s", strategyName)
	}
}

// ExecContext executes a write query (INSERT, UPDATE, DELETE)
func (c *ClusterDB) ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error) {
	return c.router.ExecContext(ctx, query, args...)
}

// QueryContext executes a read query (SELECT)
func (c *ClusterDB) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	return c.router.QueryContext(ctx, query, args...)
}

// QueryRowContext executes a query that returns at most one row
func (c *ClusterDB) QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row {
	return c.router.QueryRowContext(ctx, query, args...)
}

// BeginTx begins a transaction
// For sharded databases, use BeginTxOnShard for explicit shard selection
func (c *ClusterDB) BeginTx(ctx context.Context, opts *sql.TxOptions) (*sql.Tx, error) {
	return c.router.BeginTx(ctx, opts)
}

// BeginTxOnShard begins a transaction on a specific shard
func (c *ClusterDB) BeginTxOnShard(ctx context.Context, shardIndex int, opts *sql.TxOptions) (*sql.Tx, error) {
	return c.router.BeginTxOnShard(ctx, shardIndex, opts)
}

// PingContext verifies connections to all shards
func (c *ClusterDB) PingContext(ctx context.Context) error {
	return c.router.PingContext(ctx)
}

// Close closes all shard connections
func (c *ClusterDB) Close() error {
	return c.router.Close()
}

// Router returns the underlying router for advanced usage
func (c *ClusterDB) Router() *Router {
	return c.router
}

// GetShard returns the shard cluster at the given index
func (c *ClusterDB) GetShard(index int) (*rw.Cluster, error) {
	return c.router.GetShard(index)
}

// ShardCount returns the number of shards
func (c *ClusterDB) ShardCount() int {
	return c.router.ShardCount()
}

// Metrics returns router metrics
func (c *ClusterDB) Metrics() RouterMetrics {
	return c.router.Metrics()
}

// GetConfig returns the cluster configuration
func (c *ClusterDB) GetConfig() ClusterConfig {
	return c.config
}

// HealthCheck performs a health check on all shards
func (c *ClusterDB) HealthCheck(ctx context.Context, timeout time.Duration) ([]ShardHealth, error) {
	shardCount := c.router.ShardCount()
	results := make([]ShardHealth, shardCount)

	for i := 0; i < shardCount; i++ {
		shard, err := c.router.GetShard(i)
		if err != nil {
			results[i] = ShardHealth{
				ShardIndex: i,
				Status:     "error",
				Error:      err.Error(),
			}
			continue
		}

		start := time.Now()
		pingCtx := ctx
		var cancel context.CancelFunc
		if timeout > 0 {
			pingCtx, cancel = context.WithTimeout(ctx, timeout)
		}
		pingErr := shard.PingContext(pingCtx)
		if cancel != nil {
			cancel()
		}
		latency := time.Since(start)

		if pingErr != nil {
			results[i] = ShardHealth{
				ShardIndex: i,
				Status:     "unhealthy",
				Latency:    latency,
				Error:      pingErr.Error(),
			}
		} else {
			results[i] = ShardHealth{
				ShardIndex: i,
				Status:     "healthy",
				Latency:    latency,
			}
		}
	}

	return results, nil
}

// ShardHealth represents the health status of a shard
type ShardHealth struct {
	ShardIndex int           `json:"shard_index"`
	Status     string        `json:"status"`
	Latency    time.Duration `json:"latency,omitempty"`
	Error      string        `json:"error,omitempty"`
}
