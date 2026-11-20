package dbinsights

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func TestAggregatorRecordQuery(t *testing.T) {
	agg := NewAggregator(100 * time.Millisecond)

	agg.RecordQuery("query", "SELECT * FROM users", 50*time.Millisecond, nil)
	agg.RecordQuery("exec", "INSERT INTO users (name) VALUES ('test')", 30*time.Millisecond, nil)
	agg.RecordQuery("query", "SELECT * FROM posts", 150*time.Millisecond, nil)

	global := agg.GetGlobalStats()
	if global.TotalQueries != 3 {
		t.Errorf("expected 3 total queries, got %d", global.TotalQueries)
	}
	if global.SuccessQueries != 3 {
		t.Errorf("expected 3 success queries, got %d", global.SuccessQueries)
	}
	if global.SlowQueries != 1 {
		t.Errorf("expected 1 slow query, got %d", global.SlowQueries)
	}
}

func TestAggregatorOperationStats(t *testing.T) {
	agg := NewAggregator(100 * time.Millisecond)

	agg.RecordQuery("query", "SELECT * FROM users", 50*time.Millisecond, nil)
	agg.RecordQuery("query", "SELECT * FROM posts", 60*time.Millisecond, nil)
	agg.RecordQuery("exec", "INSERT INTO users (name) VALUES ('test')", 30*time.Millisecond, nil)

	queryStats, ok := agg.GetOperationStats("query")
	if !ok {
		t.Fatal("expected query operation stats to exist")
	}
	if queryStats.TotalQueries != 2 {
		t.Errorf("expected 2 query operations, got %d", queryStats.TotalQueries)
	}

	execStats, ok := agg.GetOperationStats("exec")
	if !ok {
		t.Fatal("expected exec operation stats to exist")
	}
	if execStats.TotalQueries != 1 {
		t.Errorf("expected 1 exec operation, got %d", execStats.TotalQueries)
	}
}

func TestAggregatorTableStats(t *testing.T) {
	agg := NewAggregator(100 * time.Millisecond)

	agg.RecordQuery("query", "SELECT * FROM users", 50*time.Millisecond, nil)
	agg.RecordQuery("query", "SELECT * FROM users WHERE id = 1", 60*time.Millisecond, nil)
	agg.RecordQuery("query", "SELECT * FROM posts", 70*time.Millisecond, nil)

	usersStats, ok := agg.GetTableStats("users")
	if !ok {
		t.Fatal("expected users table stats to exist")
	}
	if usersStats.TotalQueries != 2 {
		t.Errorf("expected 2 queries on users table, got %d", usersStats.TotalQueries)
	}

	postsStats, ok := agg.GetTableStats("posts")
	if !ok {
		t.Fatal("expected posts table stats to exist")
	}
	if postsStats.TotalQueries != 1 {
		t.Errorf("expected 1 query on posts table, got %d", postsStats.TotalQueries)
	}
}

func TestAggregatorErrorTracking(t *testing.T) {
	agg := NewAggregator(100 * time.Millisecond)

	agg.RecordQuery("query", "SELECT * FROM users", 50*time.Millisecond, nil)
	agg.RecordQuery("query", "SELECT * FROM invalid", 60*time.Millisecond, errors.New("table not found"))

	global := agg.GetGlobalStats()
	if global.SuccessQueries != 1 {
		t.Errorf("expected 1 success query, got %d", global.SuccessQueries)
	}
	if global.FailedQueries != 1 {
		t.Errorf("expected 1 failed query, got %d", global.FailedQueries)
	}
}

func TestAggregatorDurationTracking(t *testing.T) {
	agg := NewAggregator(100 * time.Millisecond)

	agg.RecordQuery("query", "SELECT * FROM users", 30*time.Millisecond, nil)
	agg.RecordQuery("query", "SELECT * FROM posts", 70*time.Millisecond, nil)
	agg.RecordQuery("query", "SELECT * FROM comments", 50*time.Millisecond, nil)

	global := agg.GetGlobalStats()
	if global.MinDuration != 30*time.Millisecond {
		t.Errorf("expected min duration 30ms, got %s", global.MinDuration)
	}
	if global.MaxDuration != 70*time.Millisecond {
		t.Errorf("expected max duration 70ms, got %s", global.MaxDuration)
	}
	if global.AvgDuration != 50*time.Millisecond {
		t.Errorf("expected avg duration 50ms, got %s", global.AvgDuration)
	}
}

func TestAggregatorQueryTypeTracking(t *testing.T) {
	agg := NewAggregator(100 * time.Millisecond)

	agg.RecordQuery("query", "SELECT * FROM users", 50*time.Millisecond, nil)
	agg.RecordQuery("exec", "INSERT INTO users (name) VALUES ('test')", 30*time.Millisecond, nil)
	agg.RecordQuery("exec", "UPDATE users SET name = 'test2' WHERE id = 1", 40*time.Millisecond, nil)
	agg.RecordQuery("exec", "DELETE FROM users WHERE id = 1", 20*time.Millisecond, nil)

	global := agg.GetGlobalStats()
	if global.SelectQueries != 1 {
		t.Errorf("expected 1 SELECT query, got %d", global.SelectQueries)
	}
	if global.InsertQueries != 1 {
		t.Errorf("expected 1 INSERT query, got %d", global.InsertQueries)
	}
	if global.UpdateQueries != 1 {
		t.Errorf("expected 1 UPDATE query, got %d", global.UpdateQueries)
	}
	if global.DeleteQueries != 1 {
		t.Errorf("expected 1 DELETE query, got %d", global.DeleteQueries)
	}
}

func TestAggregatorDoesNotDoubleCountQueryType(t *testing.T) {
	agg := NewAggregator(100 * time.Millisecond)
	agg.RecordQuery("SELECT", "SELECT * FROM users", 50*time.Millisecond, nil)

	global := agg.GetGlobalStats()
	if global.SelectQueries != 1 {
		t.Fatalf("expected 1 SELECT query, got %d", global.SelectQueries)
	}
}

func TestAggregatorTopSlowTables(t *testing.T) {
	agg := NewAggregator(50 * time.Millisecond)
	agg.RecordQuery("query", "SELECT * FROM users", 100*time.Millisecond, nil)
	agg.RecordQuery("query", "SELECT * FROM users", 150*time.Millisecond, nil)
	agg.RecordQuery("query", "SELECT * FROM posts", 80*time.Millisecond, nil)
	agg.RecordQuery("query", "SELECT * FROM comments", 200*time.Millisecond, nil)

	topTables := agg.GetTopSlowTables(2)
	if len(topTables) != 2 {
		t.Fatalf("expected 2 top tables, got %d", len(topTables))
	}
	if topTables[0].Table != "users" {
		t.Errorf("expected first table to be users, got %q", topTables[0].Table)
	}
	if topTables[0].SlowQueries != 2 {
		t.Errorf("expected 2 slow queries for users, got %d", topTables[0].SlowQueries)
	}
	if topTables[1].SlowQueries != 1 {
		t.Errorf("expected 1 slow query for second table, got %d", topTables[1].SlowQueries)
	}
}

func TestAggregatorReset(t *testing.T) {
	agg := NewAggregator(100 * time.Millisecond)
	agg.RecordQuery("query", "SELECT * FROM users", 50*time.Millisecond, nil)
	agg.RecordQuery("exec", "INSERT INTO users (name) VALUES ('test')", 30*time.Millisecond, nil)

	agg.Reset()

	if global := agg.GetGlobalStats(); global.TotalQueries != 0 {
		t.Errorf("expected 0 total queries after reset, got %d", global.TotalQueries)
	}
	if len(agg.GetAllOperationStats()) != 0 {
		t.Errorf("expected 0 operation stats after reset, got %d", len(agg.GetAllOperationStats()))
	}
	if len(agg.GetAllTableStats()) != 0 {
		t.Errorf("expected 0 table stats after reset, got %d", len(agg.GetAllTableStats()))
	}
}

func TestAggregatorSummary(t *testing.T) {
	agg := NewAggregator(100 * time.Millisecond)
	agg.RecordQuery("query", "SELECT * FROM users", 50*time.Millisecond, nil)
	agg.RecordQuery("query", "SELECT * FROM posts", 150*time.Millisecond, nil)
	agg.RecordQuery("exec", "INSERT INTO users (name) VALUES ('test')", 30*time.Millisecond, nil)

	summary := agg.Summary()
	if !strings.Contains(summary, "Database Statistics Summary") {
		t.Error("expected summary to contain title")
	}
	if !strings.Contains(summary, "Global Stats") {
		t.Error("expected summary to contain global stats")
	}
	if !strings.Contains(summary, "Stats by Operation") {
		t.Error("expected summary to contain operation stats")
	}
	if !strings.Contains(summary, "Top Slow Tables") {
		t.Error("expected summary to contain table stats")
	}
}

func TestExtractTableName(t *testing.T) {
	tests := []struct {
		query     string
		wantTable string
	}{
		{"SELECT * FROM users", "users"},
		{"SELECT id, name FROM users WHERE id = 1", "users"},
		{"SELECT * FROM `users`", "users"},
		{"SELECT * FROM \"users\"", "users"},
		{"INSERT INTO users (name) VALUES ('test')", "users"},
		{"INSERT INTO posts (title, content) VALUES ('test', 'content')", "posts"},
		{"UPDATE users SET name = 'test' WHERE id = 1", "users"},
		{"DELETE FROM users WHERE id = 1", "users"},
		{"", ""},
		{"INVALID QUERY", ""},
	}

	for _, tt := range tests {
		t.Run(tt.query, func(t *testing.T) {
			got := extractTableName(tt.query)
			if got != tt.wantTable {
				t.Errorf("extractTableName(%q) = %q, want %q", tt.query, got, tt.wantTable)
			}
		})
	}
}

func TestAggregatorPreservesZeroMinimumDuration(t *testing.T) {
	aggregator := NewAggregator(time.Second)
	const query = "SELECT * FROM records"
	assertMin := func(want time.Duration) {
		t.Helper()
		operation, hasOperation := aggregator.GetOperationStats("query")
		table, hasTable := aggregator.GetTableStats("records")
		global := aggregator.GetGlobalStats()
		if !hasOperation || !hasTable || global.MinDuration != want || operation.MinDuration != want || table.MinDuration != want {
			t.Fatalf("global=%v operation=%v table=%v; want %v", global.MinDuration, operation.MinDuration, table.MinDuration, want)
		}
	}
	aggregator.RecordQuery("query", query, 0, nil)
	aggregator.RecordQuery("query", query, time.Millisecond, nil)
	assertMin(0)
	aggregator.Reset()
	aggregator.RecordQuery("query", query, time.Millisecond, nil)
	assertMin(time.Millisecond)
}
