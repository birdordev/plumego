package sharding

import (
	"database/sql"
	"testing"
	"time"

	"github.com/birdordev/plumego/x/data/rw"
)

// BenchmarkRoutingOverhead measures the overhead of routing decisions
// Target: < 100μs per routing decision
func BenchmarkRoutingOverhead(b *testing.B) {
	// Create router with 4 shards
	shards := make([]*rw.Cluster, 4)
	for i := 0; i < 4; i++ {
		connector := &stubConnector{conn: &stubConn{}}
		primary := sql.OpenDB(connector)
		cluster, _ := rw.New(rw.Config{
			Primary: primary,
			HealthCheck: rw.HealthCheckConfig{
				Enabled: false,
			},
		})
		shards[i] = cluster
	}
	defer func() {
		for _, shard := range shards {
			shard.Close()
		}
	}()

	registry := NewShardingRuleRegistry()
	rule, _ := NewShardingRule("users", "user_id", NewModStrategy(), 4)
	registry.Register(rule)

	router, _ := NewRouter(shards, registry)

	// Test queries
	queries := []struct {
		name  string
		query string
		args  []any
	}{
		{
			name:  "INSERT",
			query: "INSERT INTO users (user_id, name) VALUES (?, ?)",
			args:  []any{100, "test"},
		},
		{
			name:  "SELECT",
			query: "SELECT * FROM users WHERE user_id = ?",
			args:  []any{100},
		},
		{
			name:  "UPDATE",
			query: "UPDATE users SET name = ? WHERE user_id = ?",
			args:  []any{"test", 100},
		},
		{
			name:  "DELETE",
			query: "DELETE FROM users WHERE user_id = ?",
			args:  []any{100},
		},
	}

	for _, q := range queries {
		b.Run(q.name, func(b *testing.B) {
			b.ResetTimer()

			var start time.Time
			var totalDuration time.Duration

			for i := 0; i < b.N; i++ {
				start = time.Now()

				// Resolve the shard (this is the routing overhead we're measuring)
				resolver := router.resolver
				_, err := resolver.Resolve(q.query, q.args)

				totalDuration += time.Since(start)

				if err != nil {
					b.Fatalf("routing failed: %v", err)
				}
			}

			// Calculate average routing time
			avgTime := totalDuration / time.Duration(b.N)
			b.ReportMetric(float64(avgTime.Microseconds()), "μs/op")

			// Report pass/fail based on 100μs target
			if avgTime > 100*time.Microsecond {
				b.Logf("WARNING: Average routing time %v exceeds 100μs target", avgTime)
			} else {
				b.Logf("PASS: Average routing time %v is within 100μs target", avgTime)
			}
		})
	}
}

// BenchmarkHashStrategyPerformance measures hash strategy performance
func BenchmarkHashStrategyPerformance(b *testing.B) {
	strategy := NewHashStrategy()
	numShards := 4

	tests := []struct {
		name string
		key  any
	}{
		{"int", 12345},
		{"int64", int64(12345)},
		{"string", "user_12345"},
		{"bytes", []byte("user_12345")},
	}

	for _, tt := range tests {
		b.Run(tt.name, func(b *testing.B) {
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				_, err := strategy.Shard(tt.key, numShards)
				if err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// BenchmarkModStrategyPerformance measures mod strategy performance
func BenchmarkModStrategyPerformance(b *testing.B) {
	strategy := NewModStrategy()
	numShards := 4

	tests := []struct {
		name string
		key  any
	}{
		{"int", 12345},
		{"int64", int64(12345)},
		{"uint64", uint64(12345)},
	}

	for _, tt := range tests {
		b.Run(tt.name, func(b *testing.B) {
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				_, err := strategy.Shard(tt.key, numShards)
				if err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// BenchmarkRangeStrategyPerformance measures range strategy performance
func BenchmarkRangeStrategyPerformance(b *testing.B) {
	ranges := []RangeDefinition{
		{Start: 0, End: 1000, Shard: 0},
		{Start: 1000, End: 2000, Shard: 1},
		{Start: 2000, End: 3000, Shard: 2},
		{Start: 3000, End: 4000, Shard: 3},
	}
	strategy, _ := NewRangeStrategy(ranges)
	numShards := 4

	tests := []struct {
		name string
		key  int
	}{
		{"first_shard", 500},
		{"middle_shard", 1500},
		{"last_shard", 3500},
	}

	for _, tt := range tests {
		b.Run(tt.name, func(b *testing.B) {
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				_, err := strategy.Shard(tt.key, numShards)
				if err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// BenchmarkListStrategyPerformance measures list strategy performance
func BenchmarkListStrategyPerformance(b *testing.B) {
	mapping := map[any]int{
		"US":   0,
		"EU":   1,
		"ASIA": 2,
	}
	strategy := NewListStrategyWithDefault(mapping, 3)
	numShards := 4

	tests := []struct {
		name string
		key  string
	}{
		{"mapped", "US"},
		{"unmapped", "UNKNOWN"},
	}

	for _, tt := range tests {
		b.Run(tt.name, func(b *testing.B) {
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				_, err := strategy.Shard(tt.key, numShards)
				if err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// BenchmarkSQLParserPerformance measures SQL parser performance
func BenchmarkSQLParserPerformance(b *testing.B) {
	parser := NewSQLParser()

	queries := []struct {
		name  string
		query string
	}{
		{
			name:  "simple_select",
			query: "SELECT * FROM users WHERE user_id = ?",
		},
		{
			name:  "complex_select",
			query: "SELECT * FROM users WHERE user_id = ? AND status = ? ORDER BY created_at DESC LIMIT 10",
		},
		{
			name:  "insert",
			query: "INSERT INTO users (user_id, name, email) VALUES (?, ?, ?)",
		},
		{
			name:  "update",
			query: "UPDATE users SET name = ?, email = ? WHERE user_id = ?",
		},
		{
			name:  "delete",
			query: "DELETE FROM users WHERE user_id = ?",
		},
	}

	for _, q := range queries {
		b.Run(q.name, func(b *testing.B) {
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				_, err := parser.Parse(q.query)
				if err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// BenchmarkEndToEndRouting measures end-to-end routing performance
func BenchmarkEndToEndRouting(b *testing.B) {
	// Create router with 4 shards
	shards := make([]*rw.Cluster, 4)
	for i := 0; i < 4; i++ {
		connector := &stubConnector{conn: &stubConn{}}
		primary := sql.OpenDB(connector)
		cluster, _ := rw.New(rw.Config{
			Primary: primary,
			HealthCheck: rw.HealthCheckConfig{
				Enabled: false,
			},
		})
		shards[i] = cluster
	}
	defer func() {
		for _, shard := range shards {
			shard.Close()
		}
	}()

	registry := NewShardingRuleRegistry()
	rule, _ := NewShardingRule("users", "user_id", NewModStrategy(), 4)
	registry.Register(rule)

	router, _ := NewRouter(shards, registry)

	ctx := b.Context()
	query := "SELECT * FROM users WHERE user_id = ?"

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		rows, err := router.QueryContext(ctx, query, i)
		if err != nil {
			b.Fatal(err)
		}
		rows.Close()
	}
}

// BenchmarkConcurrentRouting measures routing performance under concurrent load
func BenchmarkConcurrentRouting(b *testing.B) {
	// Create router with 4 shards
	shards := make([]*rw.Cluster, 4)
	for i := 0; i < 4; i++ {
		connector := &stubConnector{conn: &stubConn{}}
		primary := sql.OpenDB(connector)
		cluster, _ := rw.New(rw.Config{
			Primary: primary,
			HealthCheck: rw.HealthCheckConfig{
				Enabled: false,
			},
		})
		shards[i] = cluster
	}
	defer func() {
		for _, shard := range shards {
			shard.Close()
		}
	}()

	registry := NewShardingRuleRegistry()
	rule, _ := NewShardingRule("users", "user_id", NewModStrategy(), 4)
	registry.Register(rule)

	router, _ := NewRouter(shards, registry)

	ctx := b.Context()
	query := "SELECT * FROM users WHERE user_id = ?"

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			rows, err := router.QueryContext(ctx, query, i)
			if err != nil {
				b.Fatal(err)
			}
			rows.Close()
			i++
		}
	})
}

// BenchmarkMemoryAllocation measures memory allocations during routing
func BenchmarkMemoryAllocation(b *testing.B) {
	// Create router with 4 shards
	shards := make([]*rw.Cluster, 4)
	for i := 0; i < 4; i++ {
		connector := &stubConnector{conn: &stubConn{}}
		primary := sql.OpenDB(connector)
		cluster, _ := rw.New(rw.Config{
			Primary: primary,
			HealthCheck: rw.HealthCheckConfig{
				Enabled: false,
			},
		})
		shards[i] = cluster
	}
	defer func() {
		for _, shard := range shards {
			shard.Close()
		}
	}()

	registry := NewShardingRuleRegistry()
	rule, _ := NewShardingRule("users", "user_id", NewModStrategy(), 4)
	registry.Register(rule)

	router, _ := NewRouter(shards, registry)

	query := "SELECT * FROM users WHERE user_id = ?"
	args := []any{100}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		resolver := router.resolver
		_, err := resolver.Resolve(query, args)
		if err != nil {
			b.Fatal(err)
		}
	}
}
