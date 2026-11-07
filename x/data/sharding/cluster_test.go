package sharding

import (
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/spcent/plumego/x/data/rw"
)

func newStubSQLDB() *sql.DB {
	connector := &stubConnector{conn: &stubConn{}}
	return sql.OpenDB(connector)
}

func testSQLDB(t testing.TB) *sql.DB {
	t.Helper()
	db := newStubSQLDB()
	t.Cleanup(func() {
		_ = db.Close()
	})
	return db
}

func TestClusterConfig_Validate(t *testing.T) {
	t.Run("valid configuration", func(t *testing.T) {
		config := ClusterConfig{
			Shards: []ShardConfig{
				{
					Primary: testSQLDB(t),
				},
			},
			ShardingRules: []ShardingRuleConfig{
				{
					TableName:      "users",
					ShardKeyColumn: "user_id",
					Strategy:       "mod",
				},
			},
			CrossShardPolicy: CrossShardDeny,
		}

		if err := config.Validate(); err != nil {
			t.Errorf("expected valid config, got error: %v", err)
		}
	})

	t.Run("no shards", func(t *testing.T) {
		config := ClusterConfig{
			Shards:        []ShardConfig{},
			ShardingRules: []ShardingRuleConfig{},
		}

		if err := config.Validate(); err == nil {
			t.Error("expected error for no shards")
		}
	})

	t.Run("no sharding rules", func(t *testing.T) {
		config := ClusterConfig{
			Shards: []ShardConfig{
				{
					Primary: testSQLDB(t),
				},
			},
			ShardingRules: []ShardingRuleConfig{},
		}

		if err := config.Validate(); err == nil {
			t.Error("expected error for no sharding rules")
		}
	})

	t.Run("missing primary database", func(t *testing.T) {
		config := ClusterConfig{
			Shards: []ShardConfig{
				{
					Primary: nil,
				},
			},
			ShardingRules: []ShardingRuleConfig{
				{
					TableName:      "users",
					ShardKeyColumn: "user_id",
					Strategy:       "mod",
				},
			},
		}

		if err := config.Validate(); err == nil {
			t.Error("expected error for missing primary database")
		}
	})

	t.Run("replica weights mismatch", func(t *testing.T) {
		config := ClusterConfig{
			Shards: []ShardConfig{
				{
					Primary:        testSQLDB(t),
					Replicas:       []*sql.DB{testSQLDB(t)},
					ReplicaWeights: []int{1, 2}, // Mismatch: 2 weights for 1 replica
				},
			},
			ShardingRules: []ShardingRuleConfig{
				{
					TableName:      "users",
					ShardKeyColumn: "user_id",
					Strategy:       "mod",
				},
			},
		}

		if err := config.Validate(); err == nil {
			t.Error("expected error for replica weights mismatch")
		}
	})

	t.Run("missing table name", func(t *testing.T) {
		config := ClusterConfig{
			Shards: []ShardConfig{
				{
					Primary: testSQLDB(t),
				},
			},
			ShardingRules: []ShardingRuleConfig{
				{
					TableName:      "", // Missing
					ShardKeyColumn: "user_id",
					Strategy:       "mod",
				},
			},
		}

		if err := config.Validate(); err == nil {
			t.Error("expected error for missing table name")
		}
	})

	t.Run("missing shard key column", func(t *testing.T) {
		config := ClusterConfig{
			Shards: []ShardConfig{
				{
					Primary: testSQLDB(t),
				},
			},
			ShardingRules: []ShardingRuleConfig{
				{
					TableName:      "users",
					ShardKeyColumn: "", // Missing
					Strategy:       "mod",
				},
			},
		}

		if err := config.Validate(); err == nil {
			t.Error("expected error for missing shard key column")
		}
	})

	t.Run("missing strategy", func(t *testing.T) {
		config := ClusterConfig{
			Shards: []ShardConfig{
				{
					Primary: testSQLDB(t),
				},
			},
			ShardingRules: []ShardingRuleConfig{
				{
					TableName:      "users",
					ShardKeyColumn: "user_id",
					Strategy:       "", // Missing
				},
			},
		}

		if err := config.Validate(); err == nil {
			t.Error("expected error for missing strategy")
		}
	})

	t.Run("invalid default shard index", func(t *testing.T) {
		config := ClusterConfig{
			Shards: []ShardConfig{
				{
					Primary: testSQLDB(t),
				},
			},
			ShardingRules: []ShardingRuleConfig{
				{
					TableName:      "users",
					ShardKeyColumn: "user_id",
					Strategy:       "mod",
				},
			},
			DefaultShardIndex: 999, // Out of bounds
		}

		if err := config.Validate(); err == nil {
			t.Error("expected error for invalid default shard index")
		}
	})

	t.Run("negative default shard index below disabled sentinel", func(t *testing.T) {
		config := ClusterConfig{
			Shards: []ShardConfig{
				{
					Primary: testSQLDB(t),
				},
			},
			ShardingRules: []ShardingRuleConfig{
				{
					TableName:      "users",
					ShardKeyColumn: "user_id",
					Strategy:       "mod",
				},
			},
			DefaultShardIndex: -2,
		}

		if err := config.Validate(); err == nil {
			t.Error("expected error for default shard index below -1")
		}
	})
}

func TestDefaultClusterConfig(t *testing.T) {
	config := DefaultClusterConfig()

	if config.CrossShardPolicy != CrossShardDeny {
		t.Errorf("expected CrossShardDeny policy, got %v", config.CrossShardPolicy)
	}

	if config.DefaultShardIndex != -1 {
		t.Errorf("expected default shard index -1, got %d", config.DefaultShardIndex)
	}

	if !config.EnableMetrics {
		t.Error("expected metrics to be enabled by default")
	}
}

func TestDefaultShardConfig(t *testing.T) {
	config := DefaultShardConfig()

	if config.FallbackToPrimary {
		t.Error("expected FallbackToPrimary to be false by default")
	}

	if config.HealthCheck.Interval == 0 {
		t.Error("expected non-zero health check interval")
	}
}

// Helper function to create a test cluster DB with stub databases
func createTestClusterDB(t testing.TB, shardCount int) *ClusterDB {
	t.Helper()

	// Create configuration
	shards := make([]ShardConfig, shardCount)
	for i := 0; i < shardCount; i++ {
		shards[i] = ShardConfig{
			Primary: testSQLDB(t),
			HealthCheck: rw.HealthCheckConfig{
				Enabled: false, // Disable health checks for tests
			},
			FallbackToPrimary: true,
		}
	}

	config := ClusterConfig{
		Shards: shards,
		ShardingRules: []ShardingRuleConfig{
			{
				TableName:      "users",
				ShardKeyColumn: "user_id",
				Strategy:       "mod",
			},
		},
		CrossShardPolicy: CrossShardDeny,
		EnableMetrics:    true,
	}

	originalShards := make([]*rw.Cluster, shardCount)
	for i := 0; i < shardCount; i++ {
		cluster, _ := rw.New(rw.Config{
			Primary: testSQLDB(t),
			HealthCheck: rw.HealthCheckConfig{
				Enabled: false,
			},
			FallbackToPrimary: true,
		})
		originalShards[i] = cluster
	}

	registry := NewShardingRuleRegistry()
	rule, _ := NewShardingRule("users", "user_id", NewModStrategy(), shardCount)
	registry.Register(rule)

	router, err := NewRouter(originalShards, registry, WithCrossShardPolicy(config.CrossShardPolicy))
	if err != nil {
		t.Fatalf("failed to create router: %v", err)
	}

	return &ClusterDB{
		router: router,
		config: config,
	}
}

func TestClusterDB_ExecContext(t *testing.T) {
	cluster := createTestClusterDB(t, 4)
	defer cluster.Close()

	ctx := t.Context()

	t.Run("successful insert", func(t *testing.T) {
		query := "INSERT INTO users (user_id, name) VALUES (?, ?)"
		result, err := cluster.ExecContext(ctx, query, 100, "Alice")
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
		result, err := cluster.ExecContext(ctx, query, "Bob", 100)
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
		result, err := cluster.ExecContext(ctx, query, 100)
		if err != nil {
			t.Fatalf("ExecContext failed: %v", err)
		}

		affected, _ := result.RowsAffected()
		if affected != 1 {
			t.Errorf("expected 1 row affected, got %d", affected)
		}
	})
}

func TestClusterDB_QueryContext(t *testing.T) {
	cluster := createTestClusterDB(t, 4)
	defer cluster.Close()

	ctx := t.Context()

	t.Run("successful query", func(t *testing.T) {
		query := "SELECT * FROM users WHERE user_id = ?"
		rows, err := cluster.QueryContext(ctx, query, 100)
		if err != nil {
			t.Fatalf("QueryContext failed: %v", err)
		}
		defer rows.Close()

		if rows == nil {
			t.Error("expected rows, got nil")
		}
	})
}

func TestClusterDB_QueryRowContext(t *testing.T) {
	cluster := createTestClusterDB(t, 4)
	defer cluster.Close()

	ctx := t.Context()

	t.Run("successful query row", func(t *testing.T) {
		query := "SELECT * FROM users WHERE user_id = ?"
		row := cluster.QueryRowContext(ctx, query, 100)
		if row == nil {
			t.Error("expected row, got nil")
		}
	})
}

func TestClusterDB_BeginTx(t *testing.T) {
	cluster := createTestClusterDB(t, 4)
	defer cluster.Close()

	ctx := t.Context()

	t.Run("begin transaction without default shard", func(t *testing.T) {
		_, err := cluster.BeginTx(ctx, nil)
		if err == nil {
			t.Error("expected error when starting transaction without shard selection")
		}
	})

	t.Run("begin transaction on specific shard", func(t *testing.T) {
		tx, err := cluster.BeginTxOnShard(ctx, 0, nil)
		if err != nil {
			t.Fatalf("BeginTxOnShard failed: %v", err)
		}
		if tx == nil {
			t.Error("expected transaction, got nil")
		}
		_ = tx.Rollback()
	})

	t.Run("begin transaction on invalid shard", func(t *testing.T) {
		_, err := cluster.BeginTxOnShard(ctx, 999, nil)
		if !errors.Is(err, ErrShardNotFound) {
			t.Errorf("expected ErrShardNotFound, got %v", err)
		}
	})
}

func TestClusterDB_PingContext(t *testing.T) {
	cluster := createTestClusterDB(t, 2)
	defer cluster.Close()

	ctx := t.Context()

	err := cluster.PingContext(ctx)
	if err != nil {
		t.Errorf("PingContext failed: %v", err)
	}
}

func TestClusterDB_GetShard(t *testing.T) {
	cluster := createTestClusterDB(t, 4)
	defer cluster.Close()

	t.Run("valid shard index", func(t *testing.T) {
		shard, err := cluster.GetShard(0)
		if err != nil {
			t.Fatalf("GetShard failed: %v", err)
		}
		if shard == nil {
			t.Error("expected shard, got nil")
		}
	})

	t.Run("invalid shard index", func(t *testing.T) {
		_, err := cluster.GetShard(999)
		if !errors.Is(err, ErrShardNotFound) {
			t.Errorf("expected ErrShardNotFound, got %v", err)
		}
	})
}

func TestClusterDB_ShardCount(t *testing.T) {
	tests := []struct {
		name       string
		shardCount int
	}{
		{"single shard", 1},
		{"two shards", 2},
		{"four shards", 4},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cluster := createTestClusterDB(t, tt.shardCount)
			defer cluster.Close()

			if cluster.ShardCount() != tt.shardCount {
				t.Errorf("expected %d shards, got %d", tt.shardCount, cluster.ShardCount())
			}
		})
	}
}

func TestClusterDB_Metrics(t *testing.T) {
	cluster := createTestClusterDB(t, 4)
	defer cluster.Close()

	ctx := t.Context()

	// Execute some queries
	query := "SELECT * FROM users WHERE user_id = ?"
	_, _ = cluster.QueryContext(ctx, query, 100)
	_, _ = cluster.QueryContext(ctx, query, 200)

	metrics := cluster.Metrics()
	if metrics.TotalQueries < 2 {
		t.Errorf("expected at least 2 total queries, got %d", metrics.TotalQueries)
	}
}

func TestClusterDB_Router(t *testing.T) {
	cluster := createTestClusterDB(t, 4)
	defer cluster.Close()

	router := cluster.Router()
	if router == nil {
		t.Error("expected router, got nil")
	}

	if router.ShardCount() != 4 {
		t.Errorf("expected 4 shards, got %d", router.ShardCount())
	}
}

func TestClusterDB_GetConfig(t *testing.T) {
	cluster := createTestClusterDB(t, 2)
	defer cluster.Close()

	config := cluster.GetConfig()
	if len(config.Shards) != 2 {
		t.Errorf("expected 2 shards in config, got %d", len(config.Shards))
	}
}

func TestClusterDB_HealthCheck(t *testing.T) {
	cluster := createTestClusterDB(t, 2)
	defer cluster.Close()

	ctx := t.Context()

	health, err := cluster.HealthCheck(ctx, 5*time.Second)
	if err != nil {
		t.Errorf("HealthCheck failed: %v", err)
	}

	if len(health) != 2 {
		t.Errorf("expected 2 health results, got %d", len(health))
	}

	for i, h := range health {
		if h.ShardIndex != i {
			t.Errorf("expected shard index %d, got %d", i, h.ShardIndex)
		}
		if h.Status != "healthy" {
			t.Errorf("expected healthy status for shard %d, got %s", i, h.Status)
		}
	}
}

func TestCreateStrategy(t *testing.T) {
	t.Run("hash strategy", func(t *testing.T) {
		strategy, err := createStrategy("hash", StrategyConfig{})
		if err != nil {
			t.Fatalf("createStrategy failed: %v", err)
		}
		if strategy.Name() != "hash" {
			t.Errorf("expected hash strategy, got %s", strategy.Name())
		}
	})

	t.Run("mod strategy", func(t *testing.T) {
		strategy, err := createStrategy("mod", StrategyConfig{})
		if err != nil {
			t.Fatalf("createStrategy failed: %v", err)
		}
		if strategy.Name() != "mod" {
			t.Errorf("expected mod strategy, got %s", strategy.Name())
		}
	})

	t.Run("range strategy", func(t *testing.T) {
		config := StrategyConfig{
			Ranges: []RangeDefinition{
				{Start: 0, End: 100, Shard: 0},
				{Start: 100, End: 200, Shard: 1},
			},
		}
		strategy, err := createStrategy("range", config)
		if err != nil {
			t.Fatalf("createStrategy failed: %v", err)
		}
		if strategy.Name() != "range" {
			t.Errorf("expected range strategy, got %s", strategy.Name())
		}
	})

	t.Run("range strategy without ranges", func(t *testing.T) {
		_, err := createStrategy("range", StrategyConfig{})
		if err == nil {
			t.Error("expected error for range strategy without ranges")
		}
	})

	t.Run("list strategy", func(t *testing.T) {
		config := StrategyConfig{
			Mapping: map[string]int{
				"US": 0,
				"EU": 1,
			},
			DefaultShard: 0,
		}
		strategy, err := createStrategy("list", config)
		if err != nil {
			t.Fatalf("createStrategy failed: %v", err)
		}
		if strategy.Name() != "list" {
			t.Errorf("expected list strategy, got %s", strategy.Name())
		}
	})

	t.Run("unknown strategy", func(t *testing.T) {
		_, err := createStrategy("unknown", StrategyConfig{})
		if err == nil {
			t.Error("expected error for unknown strategy")
		}
	})
}

func BenchmarkClusterDB_ExecContext(b *testing.B) {
	cluster := createTestClusterDB(b, 4)
	defer cluster.Close()

	ctx := b.Context()
	query := "INSERT INTO users (user_id, name) VALUES (?, ?)"

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = cluster.ExecContext(ctx, query, i, "test")
	}
}

func BenchmarkClusterDB_QueryContext(b *testing.B) {
	cluster := createTestClusterDB(b, 4)
	defer cluster.Close()

	ctx := b.Context()
	query := "SELECT * FROM users WHERE user_id = ?"

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		rows, _ := cluster.QueryContext(ctx, query, i)
		if rows != nil {
			rows.Close()
		}
	}
}
