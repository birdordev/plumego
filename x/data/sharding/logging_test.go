package sharding

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/birdordev/plumego/log"
	"github.com/birdordev/plumego/x/data/rw"
)

type testShardingLogEntry struct {
	Level             string `json:"level"`
	Operation         string `json:"operation"`
	StatementRedacted bool   `json:"statement_redacted"`
	ShardKeyRedacted  bool   `json:"shard_key_redacted"`
	ShardIndex        int    `json:"shard_index"`
	Error             string `json:"error"`
	ErrorRedacted     bool   `json:"error_redacted"`
	Table             string `json:"table"`
	Policy            string `json:"policy"`
	Cached            bool   `json:"cached"`
	Component         string `json:"component"`
}

func decodeTestShardingLogEntry(t *testing.T, data []byte) testShardingLogEntry {
	t.Helper()

	var entry testShardingLogEntry
	if err := json.Unmarshal(data, &entry); err != nil {
		t.Fatalf("failed to parse JSON output: %v", err)
	}
	return entry
}

func decodeTestShardingLogFields(t *testing.T, data []byte) map[string]any {
	t.Helper()

	var fields map[string]any
	if err := json.Unmarshal(data, &fields); err != nil {
		t.Fatalf("failed to parse JSON output: %v", err)
	}
	return fields
}

func TestLoggingRouter_Creation(t *testing.T) {
	// Create a test router
	connector := &stubConnector{conn: &stubConn{}}
	primary := sql.OpenDB(connector)
	cluster, _ := rw.New(rw.Config{
		Primary: primary,
		HealthCheck: rw.HealthCheckConfig{
			Enabled: false,
		},
	})
	defer cluster.Close()

	registry := NewShardingRuleRegistry()
	router, _ := NewRouter([]*rw.Cluster{cluster}, registry)

	t.Run("with nil logger creates default JSON logger", func(t *testing.T) {
		loggingRouter := NewLoggingRouter(router, nil)

		if loggingRouter.Logger() == nil {
			t.Error("expected logger to be set")
		}

		if loggingRouter.Router() != router {
			t.Error("expected router to be the same")
		}
	})

	t.Run("with custom logger", func(t *testing.T) {
		var buf bytes.Buffer
		logger := log.NewLogger(log.LoggerConfig{
			Format: log.LoggerFormatJSON,
			Output: &buf,
			Level:  log.INFO,
		})

		loggingRouter := NewLoggingRouter(router, logger)

		if loggingRouter.Logger() != logger {
			t.Error("expected custom logger to be used")
		}
	})
}

func TestLoggingRouter_LogQuery(t *testing.T) {
	var buf bytes.Buffer
	logger := log.NewLogger(log.LoggerConfig{
		Format: log.LoggerFormatJSON,
		Output: &buf,
		Level:  log.INFO,
	})

	// Create minimal router
	connector := &stubConnector{conn: &stubConn{}}
	primary := sql.OpenDB(connector)
	cluster, _ := rw.New(rw.Config{Primary: primary})
	defer cluster.Close()

	registry := NewShardingRuleRegistry()
	router, _ := NewRouter([]*rw.Cluster{cluster}, registry)

	loggingRouter := NewLoggingRouter(router, logger)
	ctx := t.Context()

	t.Run("successful query", func(t *testing.T) {
		buf.Reset()
		loggingRouter.LogQuery(ctx, "SELECT * FROM users", 0, 10*time.Millisecond, nil)

		output := buf.String()
		if !strings.Contains(output, "query executed") {
			t.Error("expected 'query executed' message")
		}
		if strings.Contains(output, "SELECT * FROM users") {
			t.Error("raw query must not be logged")
		}

		// Verify JSON structure
		entry := decodeTestShardingLogEntry(t, buf.Bytes())

		if entry.Operation != "SELECT" {
			t.Errorf("expected operation SELECT, got %v", entry.Operation)
		}
		if !entry.StatementRedacted {
			t.Error("expected statement_redacted to be true")
		}

		if entry.ShardIndex != 0 {
			t.Errorf("expected shard_index 0, got %v", entry.ShardIndex)
		}
	})

	t.Run("failed query", func(t *testing.T) {
		buf.Reset()
		loggingRouter.LogQuery(ctx, "SELECT * FROM users", 0, 10*time.Millisecond, ErrShardNotFound)

		output := buf.String()
		if !strings.Contains(output, "query failed") {
			t.Error("expected 'query failed' message")
		}
		if !strings.Contains(output, "ERROR") {
			t.Error("expected ERROR level for failed query")
		}

		entry := decodeTestShardingLogEntry(t, buf.Bytes())

		if entry.Error == "" {
			t.Error("expected error field in output")
		}
		if !entry.ErrorRedacted {
			t.Error("expected error_redacted to be true")
		}
	})
}

func TestLoggingRouter_RedactsErrorText(t *testing.T) {
	var buf bytes.Buffer
	logger := log.NewLogger(log.LoggerConfig{
		Format: log.LoggerFormatJSON,
		Output: &buf,
		Level:  log.INFO,
	})

	connector := &stubConnector{conn: &stubConn{}}
	primary := sql.OpenDB(connector)
	cluster, _ := rw.New(rw.Config{Primary: primary})
	defer cluster.Close()

	registry := NewShardingRuleRegistry()
	router, _ := NewRouter([]*rw.Cluster{cluster}, registry)

	loggingRouter := NewLoggingRouter(router, logger)
	rawErr := errors.New("pq: SELECT * FROM users password=secret token=private")
	loggingRouter.LogQuery(t.Context(), "SELECT * FROM users", 0, time.Millisecond, rawErr)

	output := buf.String()
	for _, forbidden := range []string{"password=secret", "token=private", "SELECT * FROM users"} {
		if strings.Contains(output, forbidden) {
			t.Fatalf("log output leaked %q: %s", forbidden, output)
		}
	}

	entry := decodeTestShardingLogEntry(t, buf.Bytes())
	if entry.Error != "redacted" || !entry.ErrorRedacted {
		t.Fatalf("entry error redaction = %q/%v, want redacted/true", entry.Error, entry.ErrorRedacted)
	}
}

func TestLoggingRouter_LogShardResolution(t *testing.T) {
	var buf bytes.Buffer
	logger := log.NewLogger(log.LoggerConfig{
		Format: log.LoggerFormatJSON,
		Output: &buf,
		Level:  log.INFO,
	})

	connector := &stubConnector{conn: &stubConn{}}
	primary := sql.OpenDB(connector)
	cluster, _ := rw.New(rw.Config{Primary: primary})
	defer cluster.Close()

	registry := NewShardingRuleRegistry()
	router, _ := NewRouter([]*rw.Cluster{cluster}, registry)

	loggingRouter := NewLoggingRouter(router, logger)
	ctx := t.Context()

	loggingRouter.LogShardResolution(ctx, "users", 123, 0)

	output := buf.String()
	if !strings.Contains(output, "shard resolved") {
		t.Error("expected 'shard resolved' message")
	}
	if !strings.Contains(output, "users") {
		t.Error("expected table name in output")
	}

	entry := decodeTestShardingLogEntry(t, buf.Bytes())

	if entry.Table != "users" {
		t.Errorf("expected table 'users', got %v", entry.Table)
	}

	if !entry.ShardKeyRedacted {
		t.Error("expected shard key to be redacted")
	}

	fields := decodeTestShardingLogFields(t, buf.Bytes())
	if _, ok := fields["shard_key"]; ok {
		t.Fatal("raw shard key field must not be logged")
	}
	for key, value := range fields {
		switch value := value.(type) {
		case float64:
			if value == 123 {
				t.Fatalf("raw shard key leaked in numeric field %q", key)
			}
		case string:
			if value == "123" {
				t.Fatalf("raw shard key leaked in string field %q", key)
			}
		}
	}

	if entry.ShardIndex != 0 {
		t.Errorf("expected shard_index 0, got %v", entry.ShardIndex)
	}
}

func TestLoggingRouter_LogCrossShardQuery(t *testing.T) {
	var buf bytes.Buffer
	logger := log.NewLogger(log.LoggerConfig{
		Format: log.LoggerFormatJSON,
		Output: &buf,
		Level:  log.WARNING,
	})

	connector := &stubConnector{conn: &stubConn{}}
	primary := sql.OpenDB(connector)
	cluster, _ := rw.New(rw.Config{Primary: primary})
	defer cluster.Close()

	registry := NewShardingRuleRegistry()
	router, _ := NewRouter([]*rw.Cluster{cluster}, registry)

	loggingRouter := NewLoggingRouter(router, logger)
	ctx := t.Context()

	loggingRouter.LogCrossShardQuery(ctx, "SELECT * FROM users", "first_success")

	output := buf.String()
	if !strings.Contains(output, "cross-shard query") {
		t.Error("expected 'cross-shard query' message")
	}
	if strings.Contains(output, "SELECT * FROM users") {
		t.Error("raw query must not be logged")
	}
	if !strings.Contains(output, "WARN") {
		t.Error("expected WARN level for cross-shard query")
	}

	entry := decodeTestShardingLogEntry(t, buf.Bytes())

	if entry.Level != "WARN" {
		t.Errorf("expected level WARN, got %v", entry.Level)
	}

	if entry.Policy != "first_success" {
		t.Errorf("expected policy 'first_success', got %v", entry.Policy)
	}
}

func TestLoggingRouter_LogRewrite(t *testing.T) {
	var buf bytes.Buffer
	logger := log.NewLogger(log.LoggerConfig{
		Format: log.LoggerFormatJSON,
		Output: &buf,
		Level:  log.INFO,
	})

	connector := &stubConnector{conn: &stubConn{}}
	primary := sql.OpenDB(connector)
	cluster, _ := rw.New(rw.Config{Primary: primary})
	defer cluster.Close()

	registry := NewShardingRuleRegistry()
	router, _ := NewRouter([]*rw.Cluster{cluster}, registry)

	loggingRouter := NewLoggingRouter(router, logger)
	ctx := t.Context()

	loggingRouter.LogRewrite(ctx, "SELECT * FROM users", "SELECT * FROM users_0", true)

	output := buf.String()
	if !strings.Contains(output, "SQL rewritten") {
		t.Error("expected 'SQL rewritten' message")
	}
	if strings.Contains(output, "SELECT * FROM users") || strings.Contains(output, "users_0") {
		t.Error("raw original or rewritten SQL must not be logged")
	}

	entry := decodeTestShardingLogEntry(t, buf.Bytes())

	if entry.Operation != "SELECT" {
		t.Errorf("expected operation SELECT, got %v", entry.Operation)
	}

	if !entry.Cached {
		t.Errorf("expected cached true, got %v", entry.Cached)
	}
}

func TestLoggingRouter_DoesNotLogRequestIDFromContract(t *testing.T) {
	var buf bytes.Buffer
	logger := log.NewLogger(log.LoggerConfig{
		Format: log.LoggerFormatJSON,
		Output: &buf,
		Level:  log.INFO,
	})

	connector := &stubConnector{conn: &stubConn{}}
	primary := sql.OpenDB(connector)
	cluster, _ := rw.New(rw.Config{Primary: primary})
	defer cluster.Close()

	registry := NewShardingRuleRegistry()
	router, _ := NewRouter([]*rw.Cluster{cluster}, registry)

	loggingRouter := NewLoggingRouter(router, logger)

	ctx := t.Context()

	loggingRouter.LogQuery(ctx, "SELECT * FROM users", 0, 10*time.Millisecond, nil)

	if strings.Contains(buf.String(), "request_id") {
		t.Error("sharding logging must not depend on contract request metadata")
	}
}

func TestLoggingRouter_DefaultFields(t *testing.T) {
	var buf bytes.Buffer

	connector := &stubConnector{conn: &stubConn{}}
	primary := sql.OpenDB(connector)
	cluster, _ := rw.New(rw.Config{Primary: primary})
	defer cluster.Close()

	registry := NewShardingRuleRegistry()
	router, _ := NewRouter([]*rw.Cluster{cluster}, registry)

	// Create a custom logger with buffer and default fields
	customLogger := log.NewLogger(log.LoggerConfig{
		Format: log.LoggerFormatJSON,
		Output: &buf,
		Level:  log.INFO,
		Fields: log.Fields{"component": "sharding"},
	})
	loggingRouter := NewLoggingRouter(router, customLogger)

	ctx := t.Context()
	loggingRouter.LogQuery(ctx, "SELECT * FROM users", 0, 10*time.Millisecond, nil)

	entry := decodeTestShardingLogEntry(t, buf.Bytes())

	if entry.Component != "sharding" {
		t.Errorf("expected component field 'sharding', got %v", entry.Component)
	}
}

func BenchmarkLoggingRouter_LogQuery(b *testing.B) {
	var buf bytes.Buffer
	logger := log.NewLogger(log.LoggerConfig{
		Format: log.LoggerFormatJSON,
		Output: &buf,
		Level:  log.INFO,
	})

	connector := &stubConnector{conn: &stubConn{}}
	primary := sql.OpenDB(connector)
	cluster, _ := rw.New(rw.Config{Primary: primary})
	defer cluster.Close()

	registry := NewShardingRuleRegistry()
	router, _ := NewRouter([]*rw.Cluster{cluster}, registry)

	loggingRouter := NewLoggingRouter(router, logger)
	ctx := b.Context()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		loggingRouter.LogQuery(ctx, "SELECT * FROM users", 0, 10*time.Millisecond, nil)
	}
}
