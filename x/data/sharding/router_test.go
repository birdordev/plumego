package sharding

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/spcent/plumego/x/data/rw"
)

// Test stubs for database connections (reused from rw package tests)
type stubConnector struct {
	conn *stubConn
}

func (c *stubConnector) Connect(ctx context.Context) (driver.Conn, error) {
	return c.conn, nil
}

func (c *stubConnector) Driver() driver.Driver {
	return stubDriver{}
}

type stubDriver struct{}

func (d stubDriver) Open(name string) (driver.Conn, error) {
	return &stubConn{}, nil
}

type stubConn struct {
	pingErr    error
	queryDelay time.Duration
}

func (c *stubConn) Prepare(query string) (driver.Stmt, error) {
	return stubStmt{}, nil
}

func (c *stubConn) QueryContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	if c.queryDelay > 0 {
		select {
		case <-time.After(c.queryDelay):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return stubRows{}, nil
}

func (c *stubConn) Close() error {
	return nil
}

func (c *stubConn) Begin() (driver.Tx, error) {
	return stubTx{}, nil
}

func (c *stubConn) Ping(ctx context.Context) error {
	return c.pingErr
}

type stubStmt struct{}

func (s stubStmt) Close() error {
	return nil
}

func (s stubStmt) NumInput() int {
	return -1
}

func (s stubStmt) Exec(args []driver.Value) (driver.Result, error) {
	return stubResult{}, nil
}

func (s stubStmt) Query(args []driver.Value) (driver.Rows, error) {
	return stubRows{}, nil
}

type stubTx struct{}

func (t stubTx) Commit() error {
	return nil
}

func (t stubTx) Rollback() error {
	return nil
}

type stubResult struct{}

func (r stubResult) LastInsertId() (int64, error) {
	return 0, nil
}

func (r stubResult) RowsAffected() (int64, error) {
	return 1, nil
}

type stubRows struct{}

func (r stubRows) Columns() []string {
	return []string{"id", "name"}
}

func (r stubRows) Close() error {
	return nil
}

func (r stubRows) Next(dest []driver.Value) error {
	return errors.New("EOF")
}

// Helper function to create a stub database
func createStubDB() *sql.DB {
	connector := &stubConnector{
		conn: &stubConn{},
	}
	return sql.OpenDB(connector)
}

func createDelayedStubDB(delay time.Duration) *sql.DB {
	connector := &stubConnector{
		conn: &stubConn{queryDelay: delay},
	}
	return sql.OpenDB(connector)
}

// Helper function to create a test router with N shards
func createTestRouter(t *testing.T, shardCount int, policy CrossShardPolicy) (*Router, *ShardingRuleRegistry) {
	t.Helper()

	// Create stub databases for each shard
	shards := make([]*rw.Cluster, shardCount)
	for i := 0; i < shardCount; i++ {
		primary := createStubDB()
		cluster, err := rw.New(rw.Config{
			Primary:  primary,
			Replicas: []*sql.DB{},
			HealthCheck: rw.HealthCheckConfig{
				Enabled: false, // Disable health checks for tests
			},
			FallbackToPrimary: true,
		})
		if err != nil {
			t.Fatalf("failed to create cluster: %v", err)
		}
		shards[i] = cluster
	}

	// Create sharding rule registry
	registry := NewShardingRuleRegistry()
	rule, err := NewShardingRule("users", "user_id", NewModStrategy(), shardCount)
	if err != nil {
		t.Fatalf("failed to create sharding rule: %v", err)
	}
	if err := registry.Register(rule); err != nil {
		t.Fatalf("failed to register rule: %v", err)
	}

	// Create router
	router, err := NewRouter(shards, registry, WithCrossShardPolicy(policy))
	if err != nil {
		t.Fatalf("failed to create router: %v", err)
	}

	return router, registry
}

func TestNewRouter(t *testing.T) {
	t.Run("valid configuration", func(t *testing.T) {
		router, _ := createTestRouter(t, 4, CrossShardDeny)
		if router == nil {
			t.Fatal("expected router, got nil")
		}

		if router.ShardCount() != 4 {
			t.Errorf("expected 4 shards, got %d", router.ShardCount())
		}
	})

	t.Run("no shards", func(t *testing.T) {
		registry := NewShardingRuleRegistry()
		_, err := NewRouter([]*rw.Cluster{}, registry)
		if !errors.Is(err, ErrNoShards) {
			t.Errorf("expected ErrNoShards, got %v", err)
		}
	})

	t.Run("nil registry", func(t *testing.T) {
		primary := createStubDB()
		cluster, _ := rw.New(rw.Config{Primary: primary})
		_, err := NewRouter([]*rw.Cluster{cluster}, nil)
		if err == nil {
			t.Error("expected error for nil registry")
		}
	})

	t.Run("with options", func(t *testing.T) {
		router, _ := createTestRouter(t, 2, CrossShardFirstSuccess)
		if router.config.CrossShardPolicy != CrossShardFirstSuccess {
			t.Errorf("expected CrossShardFirstSuccess policy, got %v", router.config.CrossShardPolicy)
		}
	})

	t.Run("invalid negative default shard", func(t *testing.T) {
		primary := createStubDB()
		cluster, err := rw.New(rw.Config{Primary: primary})
		if err != nil {
			t.Fatalf("rw.New() error = %v", err)
		}
		defer cluster.Close()

		registry := NewShardingRuleRegistry()
		rule, err := NewShardingRule("users", "user_id", NewModStrategy(), 1)
		if err != nil {
			t.Fatalf("NewShardingRule() error = %v", err)
		}
		if err := registry.Register(rule); err != nil {
			t.Fatalf("registry.Register() error = %v", err)
		}

		_, err = NewRouter([]*rw.Cluster{cluster}, registry, WithDefaultShard(-2))
		if err == nil || !strings.Contains(err.Error(), "must be -1 or greater") {
			t.Fatalf("NewRouter() error = %v, want invalid default shard", err)
		}
	})
}

func TestRouterExecContext(t *testing.T) {
	router, _ := createTestRouter(t, 4, CrossShardDeny)
	defer router.Close()

	ctx := t.Context()

	t.Run("successful insert", func(t *testing.T) {
		query := "INSERT INTO users (user_id, name) VALUES (?, ?)"
		result, err := router.ExecContext(ctx, query, 100, "Alice")
		if err != nil {
			t.Fatalf("ExecContext failed: %v", err)
		}

		affected, _ := result.RowsAffected()
		if affected != 1 {
			t.Errorf("expected 1 row affected, got %d", affected)
		}
	})

	t.Run("successful update", func(t *testing.T) {
		query := "UPDATE users SET name = ? WHERE user_id = ?"
		result, err := router.ExecContext(ctx, query, "Bob", 100)
		if err != nil {
			t.Fatalf("ExecContext failed: %v", err)
		}

		affected, _ := result.RowsAffected()
		if affected != 1 {
			t.Errorf("expected 1 row affected, got %d", affected)
		}
	})

	t.Run("successful delete", func(t *testing.T) {
		query := "DELETE FROM users WHERE user_id = ?"
		result, err := router.ExecContext(ctx, query, 100)
		if err != nil {
			t.Fatalf("ExecContext failed: %v", err)
		}

		affected, _ := result.RowsAffected()
		if affected != 1 {
			t.Errorf("expected 1 row affected, got %d", affected)
		}
	})

	t.Run("invalid shard key", func(t *testing.T) {
		query := "INSERT INTO users (name) VALUES (?)"
		_, err := router.ExecContext(ctx, query, "Charlie")
		if err == nil {
			t.Error("expected error for missing shard key")
		}
	})
}

func TestRouterQueryContext(t *testing.T) {
	router, _ := createTestRouter(t, 4, CrossShardDeny)
	defer router.Close()

	ctx := t.Context()

	t.Run("single shard query", func(t *testing.T) {
		query := "SELECT * FROM users WHERE user_id = ?"
		rows, err := router.QueryContext(ctx, query, 100)
		if err != nil {
			t.Fatalf("QueryContext failed: %v", err)
		}
		defer rows.Close()

		if rows == nil {
			t.Error("expected rows, got nil")
		}
	})

	t.Run("range query is resolved before default shard fallback", func(t *testing.T) {
		router, _ := createTestRouter(t, 4, CrossShardFirst)
		defer router.Close()
		router.config.DefaultShardIndex = 2

		rows, err := router.QueryContext(ctx, "SELECT * FROM users WHERE user_id >= ? AND user_id <= ?", 0, 1)
		if err != nil {
			t.Fatalf("QueryContext failed: %v", err)
		}
		rows.Close()

		metrics := router.Metrics()
		if metrics.ShardQueryCounts[0] != 1 {
			t.Fatalf("expected resolved range to query shard 0, got counts %+v", metrics.ShardQueryCounts)
		}
		if metrics.ShardQueryCounts[2] != 0 {
			t.Fatalf("range query used default shard fallback, got counts %+v", metrics.ShardQueryCounts)
		}
	})

	t.Run("cross-shard query with deny policy", func(t *testing.T) {
		query := "SELECT * FROM users WHERE name = ?"
		_, err := router.QueryContext(ctx, query, "Alice")
		if !errors.Is(err, ErrCrossShardQuery) {
			t.Errorf("expected ErrCrossShardQuery, got %v", err)
		}
	})

	t.Run("multi-shard IN query with deny policy", func(t *testing.T) {
		query := "SELECT * FROM users WHERE user_id IN (?, ?)"
		_, err := router.QueryContext(ctx, query, 1, 2)
		if !errors.Is(err, ErrCrossShardQuery) {
			t.Errorf("expected ErrCrossShardQuery, got %v", err)
		}
	})
}

func TestRouterQueryRowContext(t *testing.T) {
	router, _ := createTestRouter(t, 4, CrossShardDeny)
	defer router.Close()

	ctx := t.Context()

	t.Run("single shard query row", func(t *testing.T) {
		query := "SELECT * FROM users WHERE user_id = ?"
		row := router.QueryRowContext(ctx, query, 100)
		if row == nil {
			t.Error("expected row, got nil")
		}

		// Try to scan (will fail due to stub, but shouldn't panic)
		var id int
		var name string
		err := row.Scan(&id, &name)
		if err == nil {
			t.Error("expected scan error from stub")
		}
	})

	t.Run("cross-shard deny returns scan error", func(t *testing.T) {
		query := "SELECT * FROM users WHERE name = ?"
		row := router.QueryRowContext(ctx, query, "Alice")

		var id int
		var name string
		err := row.Scan(&id, &name)
		if !errors.Is(err, ErrCrossShardQuery) {
			t.Fatalf("Scan error = %v, want ErrCrossShardQuery", err)
		}
	})

	t.Run("cross-shard first without default returns scan error", func(t *testing.T) {
		router, _ := createTestRouter(t, 4, CrossShardFirst)
		defer router.Close()

		row := router.QueryRowContext(ctx, "SELECT * FROM users WHERE name = ?", "Alice")
		var id int
		var name string
		err := row.Scan(&id, &name)
		if !errors.Is(err, ErrCrossShardQuery) {
			t.Fatalf("Scan error = %v, want ErrCrossShardQuery", err)
		}
	})

	t.Run("explicit default shard handles unresolved query row", func(t *testing.T) {
		router, _ := createTestRouter(t, 4, CrossShardFirst)
		defer router.Close()
		router.config.DefaultShardIndex = 2

		row := router.QueryRowContext(ctx, "SELECT * FROM users WHERE name = ?", "Alice")
		var id int
		var name string
		err := row.Scan(&id, &name)
		if errors.Is(err, ErrCrossShardQuery) {
			t.Fatalf("Scan error = %v, want routed default shard scan error", err)
		}

		metrics := router.Metrics()
		if metrics.ShardQueryCounts[2] != 1 {
			t.Fatalf("expected shard 2 to be queried once, got counts %+v", metrics.ShardQueryCounts)
		}
	})

	t.Run("default shard does not hide resolve failure", func(t *testing.T) {
		router, _ := createTestRouter(t, 4, CrossShardDeny)
		defer router.Close()
		router.config.DefaultShardIndex = 0

		row := router.QueryRowContext(ctx, "SELECT * FROM users WHERE user_id = ?")
		var id int
		var name string
		err := row.Scan(&id, &name)
		if err == nil || !strings.Contains(err.Error(), "failed to resolve shard") {
			t.Fatalf("Scan error = %v, want resolve failure", err)
		}
	})

	t.Run("invalid resolved shard returns scan error", func(t *testing.T) {
		router, _ := createTestRouter(t, 4, CrossShardDeny)
		defer router.Close()

		registry := NewShardingRuleRegistry()
		rule, _ := NewShardingRule("users", "user_id", NewModStrategy(), 8)
		if err := registry.Register(rule); err != nil {
			t.Fatalf("Register() unexpected error: %v", err)
		}
		router.resolver = NewShardKeyResolver(registry)
		router.rewriter = NewSQLRewriter(registry)

		row := router.QueryRowContext(ctx, "SELECT * FROM users WHERE user_id = ?", 7)
		var id int
		var name string
		err := row.Scan(&id, &name)
		if !errors.Is(err, ErrShardNotFound) {
			t.Fatalf("Scan error = %v, want ErrShardNotFound", err)
		}
	})

	t.Run("rewrite failure returns scan error", func(t *testing.T) {
		router, _ := createTestRouter(t, 4, CrossShardDeny)
		defer router.Close()

		rewriteRegistry := NewShardingRuleRegistry()
		rule, err := NewShardingRule("users", "user_id", NewModStrategy(), 1)
		if err != nil {
			t.Fatalf("NewShardingRule() error = %v", err)
		}
		if err := rewriteRegistry.Register(rule); err != nil {
			t.Fatalf("Register() error = %v", err)
		}
		router.rewriter = NewSQLRewriter(rewriteRegistry)

		row := router.QueryRowContext(ctx, "SELECT * FROM users WHERE user_id = ?", 3)
		var id int
		var name string
		err = row.Scan(&id, &name)
		if err == nil || !strings.Contains(err.Error(), "failed to rewrite SQL") {
			t.Fatalf("Scan error = %v, want rewrite failure", err)
		}
	})
}

func TestCrossShardPolicies(t *testing.T) {
	ctx := t.Context()

	t.Run("CrossShardDeny", func(t *testing.T) {
		router, _ := createTestRouter(t, 4, CrossShardDeny)
		defer router.Close()

		query := "SELECT * FROM users WHERE name = ?"
		_, err := router.QueryContext(ctx, query, "Alice")
		if !errors.Is(err, ErrCrossShardQuery) {
			t.Errorf("expected ErrCrossShardQuery, got %v", err)
		}
	})

	t.Run("CrossShardFirst", func(t *testing.T) {
		router, _ := createTestRouter(t, 4, CrossShardFirst)
		defer router.Close()

		query := "SELECT * FROM users WHERE name = ?"
		_, err := router.QueryContext(ctx, query, "Alice")
		if !errors.Is(err, ErrCrossShardQuery) {
			t.Fatalf("QueryContext error = %v, want ErrCrossShardQuery", err)
		}
	})

	t.Run("CrossShardFirstSuccess", func(t *testing.T) {
		router, _ := createTestRouter(t, 4, CrossShardFirstSuccess)
		defer router.Close()

		query := "SELECT * FROM users WHERE name = ?"
		rows, err := router.QueryContext(ctx, query, "Alice")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		defer rows.Close()

		if rows == nil {
			t.Error("expected rows, got nil")
		}
	})

	t.Run("CrossShardFirstSuccess returns first success before slow shard", func(t *testing.T) {
		fastDB := createDelayedStubDB(0)
		slowDB := createDelayedStubDB(200 * time.Millisecond)

		fastCluster, err := rw.New(rw.Config{
			Primary:           fastDB,
			FallbackToPrimary: true,
			HealthCheck:       rw.HealthCheckConfig{Enabled: false},
		})
		if err != nil {
			t.Fatalf("failed to create fast cluster: %v", err)
		}
		slowCluster, err := rw.New(rw.Config{
			Primary:           slowDB,
			FallbackToPrimary: true,
			HealthCheck:       rw.HealthCheckConfig{Enabled: false},
		})
		if err != nil {
			t.Fatalf("failed to create slow cluster: %v", err)
		}

		registry := NewShardingRuleRegistry()
		rule, err := NewShardingRule("users", "user_id", NewModStrategy(), 2)
		if err != nil {
			t.Fatalf("failed to create sharding rule: %v", err)
		}
		if err := registry.Register(rule); err != nil {
			t.Fatalf("failed to register rule: %v", err)
		}
		router, err := NewRouter([]*rw.Cluster{fastCluster, slowCluster}, registry, WithCrossShardPolicy(CrossShardFirstSuccess))
		if err != nil {
			t.Fatalf("failed to create router: %v", err)
		}
		defer router.Close()

		start := time.Now()
		rows, err := router.QueryContext(ctx, "SELECT * FROM users WHERE name = ?", "Alice")
		elapsed := time.Since(start)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		defer rows.Close()

		if elapsed >= 150*time.Millisecond {
			t.Fatalf("CrossShardFirstSuccess took %s, want return before slow shard finishes", elapsed)
		}
	})

	t.Run("CrossShardFirst uses first resolved shard for IN query", func(t *testing.T) {
		router, _ := createTestRouter(t, 4, CrossShardFirst)
		defer router.Close()

		rows, err := router.QueryContext(ctx, "SELECT * FROM users WHERE user_id IN (?, ?)", 1, 2)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		defer rows.Close()

		metrics := router.Metrics()
		if metrics.ShardQueryCounts[1] != 1 {
			t.Fatalf("expected shard 1 to be queried once, got counts %+v", metrics.ShardQueryCounts)
		}
		if metrics.ShardQueryCounts[0] != 0 {
			t.Fatalf("expected shard 0 not to be used for resolved IN query, got counts %+v", metrics.ShardQueryCounts)
		}
	})
}

func TestRouterBeginTx(t *testing.T) {
	router, _ := createTestRouter(t, 4, CrossShardDeny)
	defer router.Close()

	ctx := t.Context()

	t.Run("transaction without default shard", func(t *testing.T) {
		_, err := router.BeginTx(ctx, nil)
		if err == nil {
			t.Error("expected error when starting transaction without shard selection")
		}
	})

	t.Run("transaction on specific shard", func(t *testing.T) {
		tx, err := router.BeginTxOnShard(ctx, 0, nil)
		if err != nil {
			t.Fatalf("BeginTxOnShard failed: %v", err)
		}
		if tx == nil {
			t.Error("expected transaction, got nil")
		}
		_ = tx.Rollback()
	})

	t.Run("transaction on invalid shard", func(t *testing.T) {
		_, err := router.BeginTxOnShard(ctx, 999, nil)
		if !errors.Is(err, ErrShardNotFound) {
			t.Errorf("expected ErrShardNotFound, got %v", err)
		}
	})
}

func TestRouterGetShard(t *testing.T) {
	router, _ := createTestRouter(t, 4, CrossShardDeny)
	defer router.Close()

	t.Run("valid shard index", func(t *testing.T) {
		shard, err := router.GetShard(0)
		if err != nil {
			t.Fatalf("GetShard failed: %v", err)
		}
		if shard == nil {
			t.Error("expected shard, got nil")
		}
	})

	t.Run("invalid shard index", func(t *testing.T) {
		_, err := router.GetShard(999)
		if !errors.Is(err, ErrShardNotFound) {
			t.Errorf("expected ErrShardNotFound, got %v", err)
		}
	})

	t.Run("negative shard index", func(t *testing.T) {
		_, err := router.GetShard(-1)
		if !errors.Is(err, ErrShardNotFound) {
			t.Errorf("expected ErrShardNotFound, got %v", err)
		}
	})
}

func TestRouterPingContext(t *testing.T) {
	router, _ := createTestRouter(t, 2, CrossShardDeny)
	defer router.Close()

	ctx := t.Context()

	t.Run("successful ping", func(t *testing.T) {
		err := router.PingContext(ctx)
		if err != nil {
			t.Errorf("PingContext failed: %v", err)
		}
	})
}

func TestRouterMetrics(t *testing.T) {
	router, _ := createTestRouter(t, 4, CrossShardDeny)
	defer router.Close()

	ctx := t.Context()

	t.Run("query metrics", func(t *testing.T) {
		// Execute some queries
		query := "SELECT * FROM users WHERE user_id = ?"
		_, _ = router.QueryContext(ctx, query, 100)
		_, _ = router.QueryContext(ctx, query, 200)

		metrics := router.Metrics()
		if metrics.TotalQueries < 2 {
			t.Errorf("expected at least 2 total queries, got %d", metrics.TotalQueries)
		}
	})

	t.Run("shard-specific metrics", func(t *testing.T) {
		metrics := router.Metrics()
		if len(metrics.ShardQueryCounts) != 4 {
			t.Errorf("expected 4 shard counts, got %d", len(metrics.ShardQueryCounts))
		}
	})

	t.Run("cross-shard metrics", func(t *testing.T) {
		routerAll, _ := createTestRouter(t, 4, CrossShardFirstSuccess)
		defer routerAll.Close()

		query := "SELECT * FROM users WHERE name = ?"
		_, _ = routerAll.QueryContext(ctx, query, "Alice")

		var metrics RouterMetrics
		deadline := time.Now().Add(100 * time.Millisecond)
		for {
			metrics = routerAll.Metrics()
			allShardsRecorded := true
			for _, count := range metrics.ShardQueryCounts {
				if count != 1 {
					allShardsRecorded = false
					break
				}
			}
			if allShardsRecorded || time.Now().After(deadline) {
				break
			}
			time.Sleep(time.Millisecond)
		}
		if metrics.CrossShardQueries < 1 {
			t.Errorf("expected at least 1 cross-shard query, got %d", metrics.CrossShardQueries)
		}
		if metrics.SingleShardQueries != 0 {
			t.Errorf("expected cross-shard fan-out not to increment single-shard queries, got %d", metrics.SingleShardQueries)
		}
		for i, count := range metrics.ShardQueryCounts {
			if count != 1 {
				t.Errorf("expected shard %d to be queried once during fan-out, got %d", i, count)
			}
		}
	})
}

func TestRouterShardCount(t *testing.T) {
	tests := []struct {
		name       string
		shardCount int
	}{
		{"single shard", 1},
		{"two shards", 2},
		{"four shards", 4},
		{"eight shards", 8},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			router, _ := createTestRouter(t, tt.shardCount, CrossShardDeny)
			defer router.Close()

			if router.ShardCount() != tt.shardCount {
				t.Errorf("expected %d shards, got %d", tt.shardCount, router.ShardCount())
			}
		})
	}
}

func TestRouterClose(t *testing.T) {
	router, _ := createTestRouter(t, 4, CrossShardDeny)

	err := router.Close()
	if err != nil {
		t.Errorf("Close failed: %v", err)
	}

	// Attempting operations after close should fail
	// (Though our stub doesn't enforce this)
}

func TestCrossShardPolicyString(t *testing.T) {
	tests := []struct {
		policy CrossShardPolicy
		want   string
	}{
		{CrossShardDeny, "deny"},
		{CrossShardFirst, "first"},
		{CrossShardFirstSuccess, "first_success"},
		{CrossShardPolicy(999), "unknown"},
	}

	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			got := tt.policy.String()
			if got != tt.want {
				t.Errorf("String() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestQueryResolvedShardsValidatesPlanBeforeFanout(t *testing.T) {
	router, _ := createTestRouter(t, 2, CrossShardFirstSuccess)
	defer router.Close()

	_, err := router.queryResolvedShards(t.Context(), "SELECT * FROM users WHERE user_id IN (?, ?)", []any{1, 99}, []*ResolvedShard{
		{ShardIndex: 1},
		{ShardIndex: 99},
	})
	if !errors.Is(err, ErrShardNotFound) {
		t.Fatalf("queryResolvedShards error = %v, want ErrShardNotFound", err)
	}

	metrics := router.Metrics()
	if metrics.ShardQueryCounts[1] != 0 {
		t.Fatalf("fan-out started before validation completed, counts %+v", metrics.ShardQueryCounts)
	}
}

func TestRouterWithMultipleTables(t *testing.T) {
	t.Run("multiple table rules", func(t *testing.T) {
		// Create shards
		shardCount := 4
		shards := make([]*rw.Cluster, shardCount)
		for i := 0; i < shardCount; i++ {
			primary := createStubDB()
			cluster, _ := rw.New(rw.Config{
				Primary:  primary,
				Replicas: []*sql.DB{},
				HealthCheck: rw.HealthCheckConfig{
					Enabled: false,
				},
				FallbackToPrimary: true,
			})
			shards[i] = cluster
		}

		// Create registry with multiple tables
		registry := NewShardingRuleRegistry()

		// Add users table
		usersRule, _ := NewShardingRule("users", "user_id", NewModStrategy(), shardCount)
		registry.Register(usersRule)

		// Add orders table
		ordersRule, _ := NewShardingRule("orders", "order_id", NewHashStrategy(), shardCount)
		registry.Register(ordersRule)

		// Create router
		router, err := NewRouter(shards, registry)
		if err != nil {
			t.Fatalf("failed to create router: %v", err)
		}
		defer router.Close()

		ctx := t.Context()

		// Test query on users table
		query1 := "SELECT * FROM users WHERE user_id = ?"
		rows1, err := router.QueryContext(ctx, query1, 100)
		if err != nil {
			t.Errorf("users query failed: %v", err)
		}
		if rows1 != nil {
			rows1.Close()
		}

		// Test query on orders table
		query2 := "SELECT * FROM orders WHERE order_id = ?"
		rows2, err := router.QueryContext(ctx, query2, 200)
		if err != nil {
			t.Errorf("orders query failed: %v", err)
		}
		if rows2 != nil {
			rows2.Close()
		}
	})
}

func TestRouterWithDefaultShard(t *testing.T) {
	router, _ := createTestRouter(t, 4, CrossShardDeny)
	router.config.DefaultShardIndex = 0
	defer router.Close()

	ctx := t.Context()

	t.Run("query row with invalid shard key uses default", func(t *testing.T) {
		query := "SELECT * FROM users WHERE name = ?"
		row := router.QueryRowContext(ctx, query, "Alice")
		if row == nil {
			t.Error("expected row, got nil")
		}
	})

	t.Run("query with invalid shard key uses default", func(t *testing.T) {
		query := "SELECT * FROM users WHERE name = ?"
		rows, err := router.QueryContext(ctx, query, "Alice")
		if err != nil {
			t.Fatalf("QueryContext error = %v", err)
		}
		defer rows.Close()

		metrics := router.Metrics()
		if metrics.ShardQueryCounts[0] == 0 {
			t.Fatalf("expected default shard 0 to be queried, got counts %+v", metrics.ShardQueryCounts)
		}
	})
}

func BenchmarkRouterExecContext(b *testing.B) {
	router, _ := createTestRouter(&testing.T{}, 4, CrossShardDeny)
	defer router.Close()

	ctx := b.Context()
	query := "INSERT INTO users (user_id, name) VALUES (?, ?)"

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = router.ExecContext(ctx, query, i, "test")
	}
}

func BenchmarkRouterQueryContext(b *testing.B) {
	router, _ := createTestRouter(&testing.T{}, 4, CrossShardDeny)
	defer router.Close()

	ctx := b.Context()
	query := "SELECT * FROM users WHERE user_id = ?"

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		rows, _ := router.QueryContext(ctx, query, i)
		if rows != nil {
			rows.Close()
		}
	}
}
