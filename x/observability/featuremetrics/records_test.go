package featuremetrics

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/birdordev/plumego/metrics"
	"github.com/birdordev/plumego/x/observability/recordbuffer"
)

func TestDBRecord(t *testing.T) {
	tests := []struct {
		name      string
		operation string
		driver    string
		query     string
		rows      int
		duration  time.Duration
		err       error
		wantName  string
		wantTable string
	}{
		{
			name:      "query operation",
			operation: "query",
			driver:    "postgres",
			query:     "SELECT * FROM users",
			rows:      10,
			duration:  50 * time.Millisecond,
			wantName:  "db_query",
			wantTable: "users",
		},
		{
			name:      "exec operation",
			operation: "exec",
			driver:    "mysql",
			query:     "INSERT INTO users (name) VALUES (?)",
			rows:      1,
			duration:  30 * time.Millisecond,
			wantName:  "db_exec",
			wantTable: "users",
		},
		{
			name:      "unknown operation",
			operation: "unknown_operation",
			driver:    "postgres",
			query:     "SELECT 1",
			duration:  10 * time.Millisecond,
			wantName:  "db_unknown_operation",
		},
		{
			name:      "query with error",
			operation: "query",
			driver:    "postgres",
			query:     "SELECT * FROM invalid_table",
			duration:  20 * time.Millisecond,
			err:       errors.New("table does not exist"),
			wantName:  "db_query",
			wantTable: "invalid_table",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			record := DBRecord(tt.operation, tt.driver, tt.query, tt.rows, tt.duration, tt.err)
			if record.Name != tt.wantName {
				t.Fatalf("expected name %q, got %q", tt.wantName, record.Name)
			}
			if record.Value != tt.duration.Seconds() {
				t.Fatalf("expected value %f, got %f", tt.duration.Seconds(), record.Value)
			}
			if record.Labels[labelOperation] != tt.operation {
				t.Fatalf("expected operation label %q, got %q", tt.operation, record.Labels[labelOperation])
			}
			if tt.driver != "" && record.Labels[labelDriver] != tt.driver {
				t.Fatalf("expected driver label %q, got %q", tt.driver, record.Labels[labelDriver])
			}
			if record.Labels[labelTable] != tt.wantTable {
				t.Fatalf("expected table label %q, got %q", tt.wantTable, record.Labels[labelTable])
			}
			if (record.Error != nil) != (tt.err != nil) {
				t.Fatalf("expected err presence %v, got %v", tt.err != nil, record.Error != nil)
			}
		})
	}
}

func TestExtractTableName(t *testing.T) {
	tests := []struct {
		name  string
		query string
		want  string
	}{
		{"select from", "SELECT * FROM users", "users"},
		{"insert into", "INSERT INTO orders (id) VALUES (1)", "orders"},
		{"update", "UPDATE products SET price = 10", "products"},
		{"delete from", "DELETE FROM sessions", "sessions"},
		{"create table if not exists", "CREATE TABLE IF NOT EXISTS metrics (id INT)", "metrics"},
		{"quoted", `SELECT * FROM "Users"`, "Users"},
		{"schema qualified", "SELECT * FROM public.users", "users"},
		{"no table", "SELECT 1", ""},
		{"empty", "", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ExtractTableName(tt.query); got != tt.want {
				t.Fatalf("ExtractTableName(%q) = %q, want %q", tt.query, got, tt.want)
			}
		})
	}
}

func TestObserveFunctionsRecordDurationSeconds(t *testing.T) {
	tests := []struct {
		name    string
		observe func(metrics.Recorder, context.Context, time.Duration)
		want    string
	}{
		{
			name: "pubsub",
			observe: func(c metrics.Recorder, ctx context.Context, d time.Duration) {
				ObservePubSub(c, ctx, "publish", "topic", d, nil)
			},
			want: "pubsub_publish",
		},
		{
			name: "mq",
			observe: func(c metrics.Recorder, ctx context.Context, d time.Duration) {
				ObserveMQ(c, ctx, "enqueue", "queue", d, nil, false)
			},
			want: "mq_enqueue",
		},
		{
			name: "kv",
			observe: func(c metrics.Recorder, ctx context.Context, d time.Duration) {
				ObserveKV(c, ctx, "get", "key", d, nil, true)
			},
			want: "kv_get",
		},
		{
			name: "ipc",
			observe: func(c metrics.Recorder, ctx context.Context, d time.Duration) {
				ObserveIPC(c, ctx, "read", "/tmp/app.sock", "unix", 256, d, nil)
			},
			want: "ipc_read",
		},
		{
			name: "db",
			observe: func(c metrics.Recorder, ctx context.Context, d time.Duration) {
				ObserveDB(c, ctx, "query", "postgres", "SELECT 1", 1, d, nil)
			},
			want: "db_query",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			collector := recordbuffer.NewCollector()
			duration := 125 * time.Millisecond
			tt.observe(collector, t.Context(), duration)

			records := collector.GetRecords()
			if len(records) != 1 {
				t.Fatalf("expected 1 record, got %d", len(records))
			}
			if records[0].Name != tt.want {
				t.Fatalf("expected record name %q, got %q", tt.want, records[0].Name)
			}
			if records[0].Value != duration.Seconds() {
				t.Fatalf("expected value %f, got %f", duration.Seconds(), records[0].Value)
			}
		})
	}
}
