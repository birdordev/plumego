package sharding

import (
	"database/sql"
	"fmt"
	"strings"
	"testing"

	"github.com/birdordev/plumego/x/data/rw"
)

func TestTracer_StartSpan(t *testing.T) {
	config := DefaultTracingConfig()
	config.Enabled = true
	tracer := NewTracer(config)

	ctx := t.Context()

	t.Run("create span", func(t *testing.T) {
		newCtx, span := tracer.StartSpan(ctx, "test.operation")

		if span == nil {
			t.Fatal("expected span to be created")
		}

		if span.name != "test.operation" {
			t.Errorf("expected span name 'test.operation', got %s", span.name)
		}

		if newCtx == nil {
			t.Error("expected context to be returned")
		}
	})

	t.Run("disabled tracer", func(t *testing.T) {
		disabledConfig := DefaultTracingConfig()
		disabledConfig.Enabled = false
		disabledTracer := NewTracer(disabledConfig)

		_, span := disabledTracer.StartSpan(ctx, "test.operation")

		if span == nil {
			t.Fatal("expected span to be created even when disabled")
		}
	})
}

func TestSpan_SetAttribute(t *testing.T) {
	tracer := NewTracer(DefaultTracingConfig())
	ctx := t.Context()
	_, span := tracer.StartSpan(ctx, "test")

	span.SetAttribute("key1", "value1")
	span.SetAttribute("key2", 123)

	if span.attributes["key1"] != "value1" {
		t.Errorf("expected attribute 'key1' to be 'value1', got %v", span.attributes["key1"])
	}

	if span.attributes["key2"] != 123 {
		t.Errorf("expected attribute 'key2' to be 123, got %v", span.attributes["key2"])
	}
}

func TestSpan_SetAttributes(t *testing.T) {
	tracer := NewTracer(DefaultTracingConfig())
	ctx := t.Context()
	_, span := tracer.StartSpan(ctx, "test")

	attrs := map[string]any{
		"db.system":    "mysql",
		"db.statement": "SELECT * FROM users",
		"db.operation": "SELECT",
	}

	span.SetAttributes(attrs)

	for k, v := range attrs {
		if span.attributes[k] != v {
			t.Errorf("expected attribute %s to be %v, got %v", k, v, span.attributes[k])
		}
	}
}

func TestSpan_AddEvent(t *testing.T) {
	tracer := NewTracer(DefaultTracingConfig())
	ctx := t.Context()
	_, span := tracer.StartSpan(ctx, "test")

	span.AddEvent("query.start", map[string]any{
		"query": "SELECT * FROM users",
	})

	if len(span.events) != 1 {
		t.Errorf("expected 1 event, got %d", len(span.events))
	}

	if span.events[0].Name != "query.start" {
		t.Errorf("expected event name 'query.start', got %s", span.events[0].Name)
	}
}

func TestSpan_SetStatus(t *testing.T) {
	tracer := NewTracer(DefaultTracingConfig())
	ctx := t.Context()
	_, span := tracer.StartSpan(ctx, "test")

	span.SetStatus(SpanStatusOK, "success")

	if span.status.Code != SpanStatusOK {
		t.Errorf("expected status code OK, got %v", span.status.Code)
	}

	if span.status.Message != "success" {
		t.Errorf("expected status message 'success', got %s", span.status.Message)
	}
}

func TestSpan_RecordError(t *testing.T) {
	tracer := NewTracer(DefaultTracingConfig())
	ctx := t.Context()
	_, span := tracer.StartSpan(ctx, "test")

	err := ErrShardNotFound
	span.RecordError(err)

	if span.status.Code != SpanStatusError {
		t.Errorf("expected status code Error, got %v", span.status.Code)
	}

	if len(span.events) != 1 {
		t.Errorf("expected 1 error event, got %d", len(span.events))
	}

	if span.events[0].Name != "error" {
		t.Errorf("expected event name 'error', got %s", span.events[0].Name)
	}
}

func TestSpanRecordErrorRedactsMessage(t *testing.T) {
	tracer := NewTracer(TracingConfig{Enabled: true})
	_, span := tracer.StartSpan(t.Context(), "test")

	span.RecordError(fmt.Errorf("driver: SELECT * FROM users password=secret token=private"))

	if span.status.Message != "redacted" {
		t.Fatalf("status message = %q, want redacted", span.status.Message)
	}
	assertSpanDoesNotContain(t, span, "password=secret")
	assertSpanDoesNotContain(t, span, "token=private")
	assertSpanDoesNotContain(t, span, "SELECT * FROM users")
}

func TestTracingHelper_TraceQuery(t *testing.T) {
	config := DefaultTracingConfig()
	config.Enabled = true
	helper := NewTracingHelper(config)

	ctx := t.Context()
	query := "SELECT * FROM users WHERE id = ?"
	args := []any{123}

	newCtx, span := helper.TraceQuery(ctx, query, args)

	if newCtx == nil {
		t.Error("expected context to be returned")
	}

	if span == nil {
		t.Fatal("expected span to be created")
	}

	if _, ok := span.attributes["db.statement"]; ok {
		t.Fatalf("db.statement must not be recorded")
	}

	if span.attributes["db.statement.redacted"] != true {
		t.Errorf("expected db.statement.redacted to be true, got %v", span.attributes["db.statement.redacted"])
	}

	if span.attributes["db.operation"] != "SELECT" {
		t.Errorf("expected db.operation to be SELECT, got %v", span.attributes["db.operation"])
	}

	if span.attributes["db.args.count"] != len(args) {
		t.Errorf("expected db.args.count to be %d, got %v", len(args), span.attributes["db.args.count"])
	}
}

func TestTracingHelper_TraceShardResolve(t *testing.T) {
	helper := NewTracingHelper(DefaultTracingConfig())
	ctx := t.Context()

	newCtx, span := helper.TraceShardResolve(ctx, "users")

	if newCtx == nil {
		t.Error("expected context to be returned")
	}

	if span == nil {
		t.Fatal("expected span to be created")
	}

	if span.attributes["shard.table"] != "users" {
		t.Errorf("expected shard.table to be 'users', got %v", span.attributes["shard.table"])
	}
}

func TestTracingHelper_TraceSQLRewrite(t *testing.T) {
	helper := NewTracingHelper(DefaultTracingConfig())
	ctx := t.Context()

	originalSQL := "SELECT * FROM users"
	newCtx, span := helper.TraceSQLRewrite(ctx, originalSQL)

	if newCtx == nil {
		t.Error("expected context to be returned")
	}

	if span == nil {
		t.Fatal("expected span to be created")
	}

	if _, ok := span.attributes["sql.original"]; ok {
		t.Fatalf("sql.original must not be recorded")
	}

	if span.attributes["db.statement.redacted"] != true {
		t.Errorf("expected db.statement.redacted to be true, got %v", span.attributes["db.statement.redacted"])
	}

	if span.attributes["sql.rewrite"] != true {
		t.Errorf("expected sql.rewrite to be true, got %v", span.attributes["sql.rewrite"])
	}
}

func TestTracingHelper_TraceShardQuery(t *testing.T) {
	helper := NewTracingHelper(DefaultTracingConfig())
	ctx := t.Context()

	query := "SELECT * FROM users_0"
	newCtx, span := helper.TraceShardQuery(ctx, 0, query)

	if newCtx == nil {
		t.Error("expected context to be returned")
	}

	if span == nil {
		t.Fatal("expected span to be created")
	}

	if span.attributes["shard.index"] != 0 {
		t.Errorf("expected shard.index to be 0, got %v", span.attributes["shard.index"])
	}

	if _, ok := span.attributes["db.statement"]; ok {
		t.Fatalf("db.statement must not be recorded")
	}

	if span.attributes["db.statement.redacted"] != true {
		t.Errorf("expected db.statement.redacted to be true, got %v", span.attributes["db.statement.redacted"])
	}
}

func TestTracingHelper_DoesNotRecordRawSQL(t *testing.T) {
	helper := NewTracingHelper(TracingConfig{Enabled: true})
	ctx := t.Context()
	rawQueries := []string{
		"SELECT * FROM users WHERE email = 'secret@example.test'",
		"UPDATE accounts SET token = 'private-token' WHERE id = 7",
	}

	for _, query := range rawQueries {
		t.Run(getQueryOperation(query), func(t *testing.T) {
			_, querySpan := helper.TraceQuery(ctx, query, []any{"private-token"})
			assertSpanDoesNotContain(t, querySpan, query)
			assertSpanDoesNotContain(t, querySpan, "private-token")

			_, rewriteSpan := helper.TraceSQLRewrite(ctx, query)
			assertSpanDoesNotContain(t, rewriteSpan, query)

			_, shardSpan := helper.TraceShardQuery(ctx, 1, query)
			assertSpanDoesNotContain(t, shardSpan, query)
		})
	}
}

func assertSpanDoesNotContain(t *testing.T, span *Span, forbidden string) {
	t.Helper()
	for key, value := range span.attributes {
		if strings.Contains(key, forbidden) {
			t.Fatalf("attribute key leaked %q: %q", forbidden, key)
		}
		if strings.Contains(fmt.Sprint(value), forbidden) {
			t.Fatalf("attribute %q leaked %q: %v", key, forbidden, value)
		}
	}
	for _, event := range span.events {
		if strings.Contains(event.Name, forbidden) {
			t.Fatalf("event name leaked %q: %q", forbidden, event.Name)
		}
		for key, value := range event.Attributes {
			if strings.Contains(key, forbidden) {
				t.Fatalf("event attribute key leaked %q: %q", forbidden, key)
			}
			if strings.Contains(fmt.Sprint(value), forbidden) {
				t.Fatalf("event attribute %q leaked %q: %v", key, forbidden, value)
			}
		}
	}
}

func TestTracingHelper_TraceCrossShardQuery(t *testing.T) {
	helper := NewTracingHelper(DefaultTracingConfig())
	ctx := t.Context()

	newCtx, span := helper.TraceCrossShardQuery(ctx, 4, "first_success")

	if newCtx == nil {
		t.Error("expected context to be returned")
	}

	if span == nil {
		t.Fatal("expected span to be created")
	}

	if span.attributes["shard.count"] != 4 {
		t.Errorf("expected shard.count to be 4, got %v", span.attributes["shard.count"])
	}

	if span.attributes["shard.policy"] != "first_success" {
		t.Errorf("expected shard.policy to be 'first_success', got %v", span.attributes["shard.policy"])
	}
}

func TestInstrumentedRouter(t *testing.T) {
	// Create a test router
	shards := make([]*rw.Cluster, 2)
	for i := 0; i < 2; i++ {
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
	rule, _ := NewShardingRule("users", "user_id", NewModStrategy(), 2)
	registry.Register(rule)

	router, _ := NewRouter(shards, registry)

	tracingConfig := DefaultTracingConfig()
	tracingConfig.Enabled = true

	instrumented := NewInstrumentedRouter(router, true, tracingConfig)

	if instrumented.MetricsTracker() == nil {
		t.Error("expected metrics tracker to be created")
	}

	if instrumented.Router() != router {
		t.Error("expected router to be the same")
	}
}

func BenchmarkTracer_StartSpan(b *testing.B) {
	config := DefaultTracingConfig()
	config.Enabled = true
	tracer := NewTracer(config)
	ctx := b.Context()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, span := tracer.StartSpan(ctx, "test.operation")
		span.End()
	}
}

func BenchmarkSpan_SetAttribute(b *testing.B) {
	tracer := NewTracer(DefaultTracingConfig())
	ctx := b.Context()
	_, span := tracer.StartSpan(ctx, "test")

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		span.SetAttribute("key", "value")
	}
}
