package sharding

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sync"

	"github.com/birdordev/plumego/x/data/rw"
)

var (
	// ErrNoShards is returned when no shards are configured
	ErrNoShards = errors.New("sharding: no shards configured")

	// ErrShardNotFound is returned when the specified shard doesn't exist
	ErrShardNotFound = errors.New("sharding: shard not found")

	// ErrCrossShardQuery is returned when a query spans multiple shards and policy denies it
	ErrCrossShardQuery = errors.New("sharding: cross-shard query not allowed")

	// ErrAllShardsFailed is returned when all shards fail for a cross-shard query
	ErrAllShardsFailed = errors.New("sharding: all shards failed")
)

// CrossShardPolicy defines how to handle queries that span multiple shards
type CrossShardPolicy int

const (
	// CrossShardDeny rejects queries that cannot be routed to a single shard.
	CrossShardDeny CrossShardPolicy = iota

	// CrossShardFirst executes multi-shard resolved queries on the first
	// resolved shard. Unresolved queries still require an explicit default shard.
	CrossShardFirst

	// CrossShardFirstSuccess fans the query out to all shards concurrently and
	// returns the first successful *sql.Rows. Results from the remaining shards
	// are discarded. Use this for approximate or existence-style queries only;
	// it does NOT merge result sets from all shards.
	CrossShardFirstSuccess
)

// String returns the string representation of the policy
func (p CrossShardPolicy) String() string {
	switch p {
	case CrossShardDeny:
		return "deny"
	case CrossShardFirst:
		return "first"
	case CrossShardFirstSuccess:
		return "first_success"
	default:
		return "unknown"
	}
}

// RouterConfig holds configuration for the router
type RouterConfig struct {
	// CrossShardPolicy determines how to handle cross-shard queries
	CrossShardPolicy CrossShardPolicy

	// EnableMetrics enables query metrics collection
	EnableMetrics bool

	// DefaultShardIndex is used when no shard can be determined (optional, -1 to disable)
	DefaultShardIndex int
}

// DefaultRouterConfig returns default router configuration
func DefaultRouterConfig() RouterConfig {
	return RouterConfig{
		CrossShardPolicy:  CrossShardDeny,
		EnableMetrics:     true,
		DefaultShardIndex: -1,
	}
}

// Router routes SQL queries to appropriate shards
type Router struct {
	// shards contains all shard clusters (each shard has read-write splitting)
	shards []*rw.Cluster

	// resolver extracts shard keys from queries
	resolver *ShardKeyResolver

	// rewriter rewrites SQL queries with physical table names
	rewriter *SQLRewriter

	// config holds router configuration
	config RouterConfig

	// Metrics
	metrics RouterMetrics

	// Lifecycle
	mu sync.RWMutex
}

// RouterMetrics holds routing metrics
type RouterMetrics struct {
	TotalQueries       uint64
	SingleShardQueries uint64
	CrossShardQueries  uint64
	RoutingErrors      uint64
	ShardQueryCounts   []uint64 // Per-shard query counts
}

// RouterOption is a functional option for configuring Router
type RouterOption func(*Router)

// WithCrossShardPolicy sets the cross-shard query policy
func WithCrossShardPolicy(policy CrossShardPolicy) RouterOption {
	return func(r *Router) {
		r.config.CrossShardPolicy = policy
	}
}

// WithDefaultShard sets the default shard index
func WithDefaultShard(index int) RouterOption {
	return func(r *Router) {
		r.config.DefaultShardIndex = index
	}
}

// WithMetrics enables or disables metrics collection
func WithMetrics(enabled bool) RouterOption {
	return func(r *Router) {
		r.config.EnableMetrics = enabled
	}
}

// NewRouter creates a new sharding router
func NewRouter(shards []*rw.Cluster, registry *ShardingRuleRegistry, opts ...RouterOption) (*Router, error) {
	if len(shards) == 0 {
		return nil, ErrNoShards
	}

	if registry == nil {
		return nil, errors.New("sharding: registry cannot be nil")
	}

	// Validate registry
	if err := registry.ValidateAll(); err != nil {
		return nil, fmt.Errorf("sharding: invalid registry: %w", err)
	}

	router := &Router{
		shards:   shards,
		resolver: NewShardKeyResolver(registry),
		rewriter: NewSQLRewriter(registry),
		config:   DefaultRouterConfig(),
		metrics: RouterMetrics{
			ShardQueryCounts: make([]uint64, len(shards)),
		},
	}

	// Apply options
	for _, opt := range opts {
		opt(router)
	}

	// Validate configuration
	if router.config.DefaultShardIndex < -1 {
		return nil, fmt.Errorf("default shard index %d must be -1 or greater",
			router.config.DefaultShardIndex)
	}
	if router.config.DefaultShardIndex >= len(shards) {
		return nil, fmt.Errorf("default shard index %d exceeds shard count %d",
			router.config.DefaultShardIndex, len(shards))
	}

	return router, nil
}

// ExecContext executes a write query (INSERT, UPDATE, DELETE)
// Routes to the primary of the appropriate shard
func (r *Router) ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error) {
	r.recordQuery()

	resolvedShards, err := r.resolver.ResolveMultiple(query, args)
	if err != nil {
		r.recordRoutingError()
		return nil, fmt.Errorf("failed to resolve shard: %w", err)
	}
	if len(resolvedShards) != 1 {
		r.recordRoutingError()
		return nil, ErrCrossShardQuery
	}
	resolved := resolvedShards[0]

	// Validate shard index
	if resolved.ShardIndex < 0 || resolved.ShardIndex >= len(r.shards) {
		r.recordRoutingError()
		return nil, fmt.Errorf("%w: index %d", ErrShardNotFound, resolved.ShardIndex)
	}

	// Record metrics
	r.recordShardQuery(resolved.ShardIndex)

	// Rewrite SQL if needed (for physical table names)
	rewrittenQuery, err := r.rewriter.Rewrite(query, resolved.ShardIndex)
	if err != nil {
		r.recordRoutingError()
		return nil, fmt.Errorf("failed to rewrite SQL: %w", err)
	}

	// Execute on the resolved shard (will use primary)
	shard := r.shards[resolved.ShardIndex]
	return shard.ExecContext(ctx, rewrittenQuery, args...)
}

// QueryContext executes a read query (SELECT)
// Routes to a replica (or primary) of the appropriate shard
func (r *Router) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	r.recordQuery()

	// Check if we can resolve to a single shard
	canResolve, err := r.resolver.CanResolve(query)
	if err != nil {
		r.recordRoutingError()
		return nil, fmt.Errorf("failed to check resolvability: %w", err)
	}

	if !canResolve {
		if r.config.DefaultShardIndex >= 0 {
			return r.queryOneResolvedShard(ctx, query, args, &ResolvedShard{ShardIndex: r.config.DefaultShardIndex})
		}
		// Cannot resolve to a shard - handle according to cross-shard policy.
		return r.handleCrossShardQuery(ctx, query, args)
	}

	resolvedShards, err := r.resolver.ResolveMultiple(query, args)
	if err != nil {
		r.recordRoutingError()
		return nil, fmt.Errorf("failed to resolve shard: %w", err)
	}
	if len(resolvedShards) != 1 {
		return r.handleResolvedShardsQuery(ctx, query, args, resolvedShards)
	}
	resolved := resolvedShards[0]

	// Validate shard index
	if resolved.ShardIndex < 0 || resolved.ShardIndex >= len(r.shards) {
		r.recordRoutingError()
		return nil, fmt.Errorf("%w: index %d", ErrShardNotFound, resolved.ShardIndex)
	}

	// Record metrics
	r.recordShardQuery(resolved.ShardIndex)

	// Rewrite SQL if needed (for physical table names)
	rewrittenQuery, err := r.rewriter.Rewrite(query, resolved.ShardIndex)
	if err != nil {
		r.recordRoutingError()
		return nil, fmt.Errorf("failed to rewrite SQL: %w", err)
	}

	// Execute on the resolved shard (may use replica)
	shard := r.shards[resolved.ShardIndex]
	return shard.QueryContext(ctx, rewrittenQuery, args...)
}

// QueryRowContext executes a query that returns at most one row.
// Routes to a replica (or primary) of the appropriate shard.
// Routing failures are surfaced through the returned row's Scan error instead
// of falling back to a default shard.
func (r *Router) QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row {
	r.recordQuery()

	canResolve, err := r.resolver.CanResolve(query)
	if err != nil {
		r.recordRoutingError()
		return queryRowError(fmt.Errorf("failed to check resolvability: %w", err))
	}

	if !canResolve {
		r.recordCrossShardQuery()
		if r.config.DefaultShardIndex >= 0 {
			return r.queryRowOneResolvedShard(ctx, query, args, &ResolvedShard{ShardIndex: r.config.DefaultShardIndex})
		}
		switch r.config.CrossShardPolicy {
		case CrossShardDeny:
			return queryRowError(ErrCrossShardQuery)
		case CrossShardFirst:
			return queryRowError(ErrCrossShardQuery)
		case CrossShardFirstSuccess:
			return queryRowError(errors.New("sharding: QueryRowContext does not support CrossShardFirstSuccess"))
		default:
			return queryRowError(fmt.Errorf("unknown cross-shard policy: %v", r.config.CrossShardPolicy))
		}
	}

	resolvedShards, err := r.resolver.ResolveMultiple(query, args)
	if err != nil {
		r.recordRoutingError()
		return queryRowError(fmt.Errorf("failed to resolve shard: %w", err))
	}
	if len(resolvedShards) != 1 {
		return r.queryRowResolvedShards(ctx, query, args, resolvedShards)
	}
	resolved := resolvedShards[0]

	// Validate shard index.
	if resolved.ShardIndex < 0 || resolved.ShardIndex >= len(r.shards) {
		r.recordRoutingError()
		return queryRowError(fmt.Errorf("%w: index %d", ErrShardNotFound, resolved.ShardIndex))
	}

	// Record metrics.
	r.recordShardQuery(resolved.ShardIndex)

	// Rewrite SQL if needed (for physical table names).
	rewrittenQuery, err := r.rewriter.Rewrite(query, resolved.ShardIndex)
	if err != nil {
		r.recordRoutingError()
		return queryRowError(fmt.Errorf("failed to rewrite SQL: %w", err))
	}

	// Execute on the resolved shard.
	shard := r.shards[resolved.ShardIndex]
	return shard.QueryRowContext(ctx, rewrittenQuery, args...)
}

// BeginTx begins a transaction on a specific shard
// The shard must be determined before starting the transaction
// For sharded transactions, use BeginTxOnShard with explicit shard index
func (r *Router) BeginTx(ctx context.Context, opts *sql.TxOptions) (*sql.Tx, error) {
	// For now, transactions require explicit shard selection
	// Use default shard if configured
	if r.config.DefaultShardIndex >= 0 {
		return r.BeginTxOnShard(ctx, r.config.DefaultShardIndex, opts)
	}

	return nil, errors.New("sharding: cannot begin transaction without explicit shard selection")
}

// BeginTxOnShard begins a transaction on a specific shard
func (r *Router) BeginTxOnShard(ctx context.Context, shardIndex int, opts *sql.TxOptions) (*sql.Tx, error) {
	if shardIndex < 0 || shardIndex >= len(r.shards) {
		return nil, fmt.Errorf("%w: index %d", ErrShardNotFound, shardIndex)
	}

	shard := r.shards[shardIndex]
	return shard.BeginTx(ctx, opts)
}

// PingContext checks connectivity to all shards
func (r *Router) PingContext(ctx context.Context) error {
	var errs []error

	for i, shard := range r.shards {
		if err := shard.PingContext(ctx); err != nil {
			errs = append(errs, fmt.Errorf("shard %d: %w", i, err))
		}
	}

	if len(errs) > 0 {
		return fmt.Errorf("ping failed: %w", errors.Join(errs...))
	}

	return nil
}

// Close closes all shard connections
func (r *Router) Close() error {
	var errs []error

	for i, shard := range r.shards {
		if err := shard.Close(); err != nil {
			errs = append(errs, fmt.Errorf("shard %d: %w", i, err))
		}
	}

	if len(errs) > 0 {
		return fmt.Errorf("close failed: %w", errors.Join(errs...))
	}

	return nil
}

// GetShard returns the shard cluster at the given index
func (r *Router) GetShard(index int) (*rw.Cluster, error) {
	if index < 0 || index >= len(r.shards) {
		return nil, fmt.Errorf("%w: index %d", ErrShardNotFound, index)
	}
	return r.shards[index], nil
}

// ShardCount returns the number of shards
func (r *Router) ShardCount() int {
	return len(r.shards)
}

// Metrics returns a copy of router metrics
func (r *Router) Metrics() RouterMetrics {
	r.mu.RLock()
	defer r.mu.RUnlock()

	// Deep copy shard counts
	shardCounts := make([]uint64, len(r.metrics.ShardQueryCounts))
	copy(shardCounts, r.metrics.ShardQueryCounts)

	return RouterMetrics{
		TotalQueries:       r.metrics.TotalQueries,
		SingleShardQueries: r.metrics.SingleShardQueries,
		CrossShardQueries:  r.metrics.CrossShardQueries,
		RoutingErrors:      r.metrics.RoutingErrors,
		ShardQueryCounts:   shardCounts,
	}
}

// recordQuery increments total query count only.
// SingleShardQueries is incremented separately in recordShardQuery to avoid
// double-counting cross-shard queries.
func (r *Router) recordQuery() {
	if !r.config.EnableMetrics {
		return
	}
	r.mu.Lock()
	r.metrics.TotalQueries++
	r.mu.Unlock()
}

// recordCrossShardQuery increments cross-shard query count
func (r *Router) recordCrossShardQuery() {
	if !r.config.EnableMetrics {
		return
	}
	r.mu.Lock()
	r.metrics.CrossShardQueries++
	r.mu.Unlock()
}

// recordRoutingError increments routing error count
func (r *Router) recordRoutingError() {
	if !r.config.EnableMetrics {
		return
	}
	r.mu.Lock()
	r.metrics.RoutingErrors++
	r.mu.Unlock()
}

// recordShardQuery increments query count for a specific shard and the
// SingleShardQueries total. It must be called only for queries routed to
// exactly one shard (not cross-shard fan-out).
func (r *Router) recordShardQuery(shardIndex int) {
	if !r.config.EnableMetrics {
		return
	}
	r.mu.Lock()
	r.metrics.SingleShardQueries++
	if shardIndex >= 0 && shardIndex < len(r.metrics.ShardQueryCounts) {
		r.metrics.ShardQueryCounts[shardIndex]++
	}
	r.mu.Unlock()
}

func (r *Router) recordShardExecution(shardIndex int) {
	if !r.config.EnableMetrics {
		return
	}
	r.mu.Lock()
	if shardIndex >= 0 && shardIndex < len(r.metrics.ShardQueryCounts) {
		r.metrics.ShardQueryCounts[shardIndex]++
	}
	r.mu.Unlock()
}
