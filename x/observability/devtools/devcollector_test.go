package devtools

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/birdordev/plumego/metrics"
)

func TestRedactSQL(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "single-quoted string",
			input:    "SELECT * FROM users WHERE email = 'john@example.com'",
			expected: "SELECT * FROM users WHERE email = '?'",
		},
		{
			name:     "double-quoted string",
			input:    `SELECT * FROM users WHERE name = "John"`,
			expected: `SELECT * FROM users WHERE name = "?"`,
		},
		{
			name:     "backtick identifier",
			input:    "SELECT `password_hash` FROM `users`",
			expected: "SELECT `?` FROM `?`",
		},
		{
			name:     "single-line comment",
			input:    "SELECT * FROM users -- admin password is s3cr3t",
			expected: "SELECT * FROM users -- ?",
		},
		{
			name:     "block comment",
			input:    "SELECT /* secret_column */ * FROM users",
			expected: "SELECT /* ? */ * FROM users",
		},
		{
			name:     "numeric literal",
			input:    "SELECT * FROM users WHERE id = 12345",
			expected: "SELECT * FROM users WHERE id = ?",
		},
		{
			name:     "mixed quotes and numbers",
			input:    `SELECT * FROM users WHERE name = "John" AND age = 30 AND email = 'j@x.com'`,
			expected: `SELECT * FROM users WHERE name = "?" AND age = ? AND email = '?'`,
		},
		{
			name:     "escaped single quote",
			input:    "SELECT * FROM users WHERE name = 'O\\'Brien'",
			expected: "SELECT * FROM users WHERE name = '?'",
		},
		{
			name:     "empty query",
			input:    "",
			expected: "",
		},
		{
			name:     "comment at end with newline",
			input:    "SELECT 1 -- comment\nSELECT 2",
			expected: "SELECT ? -- ? SELECT ?",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := redactSQL(tt.input)
			if got != tt.expected {
				t.Errorf("redactSQL(%q)\n  got:  %q\n  want: %q", tt.input, got, tt.expected)
			}
		})
	}
}

func TestDevCollectorSnapshot(t *testing.T) {
	cfg := DevCollectorConfig{
		Window:     time.Minute,
		MaxSamples: 2,
		MaxSeries:  10,
		MaxValues:  100,
		DBMaxSlow:  5,
		DBSlowMS:   1,
	}

	collector := NewDevCollector(cfg)
	ctx := t.Context()

	collector.ObserveHTTP(ctx, "GET", "/hello", 200, 128, 10*time.Millisecond)
	collector.ObserveHTTP(ctx, "GET", "/hello", 404, 64, 20*time.Millisecond)
	collector.ObserveHTTP(ctx, "POST", "/submit", 500, 32, 30*time.Millisecond)

	snapshot := collector.Snapshot()

	if snapshot.Total.Count != 3 {
		t.Fatalf("expected total count 3, got %d", snapshot.Total.Count)
	}
	if snapshot.Total.ErrorCount != 2 {
		t.Fatalf("expected error count 2, got %d", snapshot.Total.ErrorCount)
	}
	if snapshot.Total.Duration.Mean < 19.9 || snapshot.Total.Duration.Mean > 20.1 {
		t.Fatalf("expected mean duration ~20ms, got %.2f", snapshot.Total.Duration.Mean)
	}
	if len(snapshot.Recent) != 2 {
		t.Fatalf("expected recent samples limited to 2, got %d", len(snapshot.Recent))
	}

	collector.ObserveDB(ctx, "query", "sqlite", "select 1", 1, 5*time.Millisecond, nil)
	dbSnapshot := collector.DBSnapshot()
	if dbSnapshot.Total.Count != 1 {
		t.Fatalf("expected db total count 1, got %d", dbSnapshot.Total.Count)
	}
	if len(dbSnapshot.Slow) == 0 {
		t.Fatalf("expected slow query sample")
	}
}

func TestDevCollectorDBTableSeries(t *testing.T) {
	cfg := DevCollectorConfig{
		Window:      time.Minute,
		MaxSamples:  10,
		MaxSeries:   10,
		MaxValues:   100,
		DBMaxSlow:   10,
		DBSlowMS:    1000, // high threshold so we don't trigger slow query
		DBMaxSeries: 10,
	}

	collector := NewDevCollector(cfg)
	ctx := t.Context()

	// Record queries against different tables
	collector.ObserveDB(ctx, "query", "postgres", "SELECT * FROM users WHERE id = 1", 1, 10*time.Millisecond, nil)
	collector.ObserveDB(ctx, "query", "postgres", "SELECT * FROM users WHERE active = true", 5, 20*time.Millisecond, nil)
	collector.ObserveDB(ctx, "exec", "postgres", "INSERT INTO orders (user_id, total) VALUES (1, 100)", 1, 15*time.Millisecond, nil)
	collector.ObserveDB(ctx, "exec", "postgres", "UPDATE products SET stock = 0 WHERE id = 5", 1, 25*time.Millisecond, nil)

	snap := collector.DBSnapshot()

	// Verify total
	if snap.Total.Count != 4 {
		t.Fatalf("expected total count 4, got %d", snap.Total.Count)
	}

	// Verify table series exist
	if len(snap.Tables) != 3 {
		t.Fatalf("expected 3 table series (users, orders, products), got %d", len(snap.Tables))
	}

	// Tables should be sorted by count descending
	// "users" has 2 queries, "orders" and "products" have 1 each
	if snap.Tables[0].Table != "users" {
		t.Errorf("expected first table to be 'users', got %q", snap.Tables[0].Table)
	}
	if snap.Tables[0].Count != 2 {
		t.Errorf("expected users count 2, got %d", snap.Tables[0].Count)
	}
}

func TestDevCollectorDBSlowQueryTable(t *testing.T) {
	cfg := DevCollectorConfig{
		Window:      time.Minute,
		MaxSamples:  10,
		MaxSeries:   10,
		MaxValues:   100,
		DBMaxSlow:   10,
		DBSlowMS:    1, // 1ms threshold
		DBMaxSeries: 10,
	}

	collector := NewDevCollector(cfg)
	ctx := t.Context()

	collector.ObserveDB(ctx, "query", "postgres", "SELECT * FROM users WHERE id = 1", 1, 5*time.Millisecond, nil)

	snap := collector.DBSnapshot()
	if len(snap.Slow) == 0 {
		t.Fatalf("expected slow query sample")
	}

	if snap.Slow[0].Table != "users" {
		t.Errorf("expected slow query table 'users', got %q", snap.Slow[0].Table)
	}
}

func TestDevCollectorDBTableSeriesClear(t *testing.T) {
	cfg := DefaultDevCollectorConfig()
	collector := NewDevCollector(cfg)
	ctx := t.Context()

	collector.ObserveDB(ctx, "query", "postgres", "SELECT * FROM users", 1, 10*time.Millisecond, nil)

	snap := collector.DBSnapshot()
	if len(snap.Tables) == 0 {
		t.Fatalf("expected table series before clear")
	}

	collector.Clear()

	snap = collector.DBSnapshot()
	if len(snap.Tables) != 0 {
		t.Fatalf("expected no table series after clear, got %d", len(snap.Tables))
	}
}

func TestDevCollectorDBTableNoQuery(t *testing.T) {
	cfg := DefaultDevCollectorConfig()
	collector := NewDevCollector(cfg)
	ctx := t.Context()

	// Operations without SQL (ping, connect, close) should not create table series
	collector.ObserveDB(ctx, "ping", "postgres", "", 0, 2*time.Millisecond, nil)
	collector.ObserveDB(ctx, "connect", "postgres", "", 0, 5*time.Millisecond, nil)

	snap := collector.DBSnapshot()
	if len(snap.Tables) != 0 {
		t.Fatalf("expected no table series for operations without queries, got %d", len(snap.Tables))
	}
}

func TestDevCollectorDurationUnitConsistency(t *testing.T) {
	tests := []struct {
		name           string
		observe        func(*DevCollector, context.Context, time.Duration)
		expectedRecord int
		assertSnapshot func(*testing.T, *DevCollector, time.Duration)
	}{
		{
			name: "http",
			observe: func(c *DevCollector, ctx context.Context, d time.Duration) {
				c.ObserveHTTP(ctx, "GET", "/unit", 200, 256, d)
			},
			expectedRecord: 1,
			assertSnapshot: func(t *testing.T, c *DevCollector, d time.Duration) {
				snap := c.Snapshot()
				expectedMS := d.Seconds() * 1000
				if snap.Total.Duration.Mean != expectedMS {
					t.Fatalf("expected HTTP snapshot mean %.3fms, got %.3f", expectedMS, snap.Total.Duration.Mean)
				}
			},
		},
		{
			name: "db",
			observe: func(c *DevCollector, ctx context.Context, d time.Duration) {
				c.ObserveDB(ctx, "query", "postgres", "SELECT * FROM users", 1, d, nil)
			},
			expectedRecord: 1,
			assertSnapshot: func(t *testing.T, c *DevCollector, d time.Duration) {
				snap := c.DBSnapshot()
				expectedMS := d.Seconds() * 1000
				if snap.Total.Duration.Mean != expectedMS {
					t.Fatalf("expected DB snapshot mean %.3fms, got %.3f", expectedMS, snap.Total.Duration.Mean)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			collector := NewDevCollector(DefaultDevCollectorConfig())
			duration := 125 * time.Millisecond
			tt.observe(collector, t.Context(), duration)

			records := collector.base.GetRecords()
			if len(records) != tt.expectedRecord {
				t.Fatalf("expected %d base records, got %d", tt.expectedRecord, len(records))
			}
			for i, record := range records {
				if record.Value != duration.Seconds() {
					t.Fatalf("record %d: expected %f seconds, got %f", i, duration.Seconds(), record.Value)
				}
			}

			tt.assertSnapshot(t, collector, duration)
		})
	}
}

func TestDevCollectorSnapshotRetentionWindow(t *testing.T) {
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	current := base

	collector := NewDevCollector(DevCollectorConfig{
		Window:     30 * time.Second,
		MaxSamples: 10,
		MaxSeries:  10,
		MaxValues:  100,
		DBMaxSlow:  5,
		DBSlowMS:   1,
	})
	collector.startedAt = base
	collector.now = func() time.Time { return current }

	ctx := t.Context()

	collector.ObserveHTTP(ctx, "GET", "/expired", 200, 10, 5*time.Millisecond)
	current = base.Add(20 * time.Second)
	collector.ObserveHTTP(ctx, "GET", "/active", 200, 20, 10*time.Millisecond)
	current = base.Add(40 * time.Second)
	collector.ObserveHTTP(ctx, "GET", "/active", 500, 30, 15*time.Millisecond)

	snapshot := collector.Snapshot()

	if len(snapshot.Recent) != 2 {
		t.Fatalf("expected 2 recent samples after retention pruning, got %d", len(snapshot.Recent))
	}
	for _, sample := range snapshot.Recent {
		if sample.Path == "/expired" {
			t.Fatalf("expected expired sample to be pruned")
		}
	}

	if len(snapshot.Routes) != 1 {
		t.Fatalf("expected only active route series, got %d", len(snapshot.Routes))
	}
	if snapshot.Routes[0].Path != "/active" {
		t.Fatalf("expected active route series, got %q", snapshot.Routes[0].Path)
	}
}

func TestDevCollectorDBSnapshotRetentionWindow(t *testing.T) {
	base := time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)
	current := base

	collector := NewDevCollector(DevCollectorConfig{
		Window:      30 * time.Second,
		MaxSamples:  10,
		MaxSeries:   10,
		MaxValues:   100,
		DBSlowMS:    1,
		DBMaxSlow:   10,
		DBMaxSeries: 10,
	})
	collector.startedAt = base
	collector.now = func() time.Time { return current }

	ctx := t.Context()

	collector.ObserveDB(ctx, "query", "mysql", "SELECT * FROM expired_users", 1, 5*time.Millisecond, nil)
	current = base.Add(20 * time.Second)
	collector.ObserveDB(ctx, "query", "postgres", "SELECT * FROM users", 1, 6*time.Millisecond, nil)
	current = base.Add(40 * time.Second)
	collector.ObserveDB(ctx, "exec", "postgres", "UPDATE orders SET total = 1", 1, 7*time.Millisecond, nil)

	snapshot := collector.DBSnapshot()

	for _, sample := range snapshot.Slow {
		if sample.Table == "expired_users" {
			t.Fatalf("expected expired slow query sample to be pruned")
		}
	}

	for _, series := range snapshot.Series {
		if series.Operation == "query" && series.Driver == "mysql" {
			t.Fatalf("expected expired db series to be pruned")
		}
	}

	for _, series := range snapshot.Tables {
		if series.Table == "expired_users" {
			t.Fatalf("expected expired db table series to be pruned")
		}
	}
}

func TestDevCollectorClearOrdersHTTPAndDBObservations(t *testing.T) {
	for _, name := range []string{"HTTP", "DB"} {
		t.Run(name, func(t *testing.T) {
			cfg := DefaultDevCollectorConfig()
			cfg.MaxSamples = 2
			cfg.DBMaxSlow = 2
			d := NewDevCollector(cfg)
			d.startedAt = time.Unix(99, 0)
			observe := func(label string) {
				if name == "HTTP" {
					d.ObserveHTTP(t.Context(), "GET", "/"+label, 200, 1, time.Millisecond)
				} else {
					d.ObserveDB(t.Context(), label, "fixture", "SELECT * FROM items", 1, time.Second, nil)
				}
			}
			entered, resume, finished := make(chan struct{}), make(chan struct{}), make(chan struct{})
			var calls atomic.Int32
			d.now = func() time.Time {
				if calls.Add(1) == 1 {
					close(entered)
					<-resume
					return time.Unix(100, 0)
				}
				return time.Unix(101, 0)
			}
			go func() { defer close(finished); observe("first") }()
			<-entered
			d.Clear()
			close(resume)
			<-finished

			assertTotal := func(want int64) {
				t.Helper()
				total := d.Snapshot().Total.Count
				if name == "DB" {
					total = d.DBSnapshot().Total.Count
				}
				stats := d.GetStats().TotalRecords
				if total != want || stats != want {
					t.Errorf("snapshot=%d stats=%d want=%d", total, stats, want)
				}
			}
			assertTotal(1)
			if name == "HTTP" && len(d.Snapshot().Recent) != 1 {
				t.Error("observation committed after Clear was pruned")
			}
			if name == "DB" && len(d.DBSnapshot().Slow) != 1 {
				t.Error("DB observation committed after Clear was pruned")
			}
			observe("second")
			observe("third")
			assertTotal(3)
			if name == "HTTP" {
				got := d.Snapshot().Recent
				if len(got) != 2 || got[0].Path != "/second" || got[1].Path != "/third" {
					t.Errorf("HTTP retention=%+v", got)
				}
			} else {
				got := d.DBSnapshot().Slow
				if len(got) != 2 || got[0].Operation != "second" || got[1].Operation != "third" {
					t.Errorf("DB retention=%+v", got)
				}
			}
			d.Clear()
			assertTotal(0)
		})
	}
}

func TestDevCollectorRecordDelegates(t *testing.T) {
	collector := NewDevCollector(DevCollectorConfig{})
	timestamp := time.Unix(1000, 0)
	record := metrics.MetricRecord{
		Name: "fixture.record", Value: 4, Timestamp: timestamp,
		Labels: metrics.MetricLabels{"kind": "original"},
	}
	collector.Record(t.Context(), record)
	record.Labels["kind"] = "changed"

	stats := collector.GetStats()
	if stats.TotalRecords != 1 || stats.NameBreakdown[record.Name] != 1 {
		t.Errorf("aggregate stats = %+v", stats)
	}
	entries := collector.base.GetRecords()
	if len(entries) != 1 {
		t.Fatalf("record buffer = %+v", entries)
	}
	if got := entries[0]; got.Name != record.Name || got.Value != record.Value ||
		got.Labels["kind"] != "original" || !got.Timestamp.Equal(timestamp) {
		t.Errorf("buffered record = %+v", got)
	}
	if snapshot := collector.Snapshot(); len(snapshot.Recent) != 0 {
		t.Errorf("ordinary record created HTTP samples: %+v", snapshot.Recent)
	}
}
