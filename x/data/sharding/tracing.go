package sharding

import (
	"context"
	"fmt"
	"strings"
)

// TracingConfig holds configuration for distributed tracing
type TracingConfig struct {
	// Enabled controls whether tracing is enabled
	Enabled bool

	// ServiceName is the name of this service in traces
	ServiceName string

	// SampleRate is the sampling rate (0.0 to 1.0)
	// 0.0 = no sampling, 1.0 = sample everything
	SampleRate float64
}

// DefaultTracingConfig returns default tracing configuration
func DefaultTracingConfig() TracingConfig {
	return TracingConfig{
		Enabled:     false,
		ServiceName: "plumego-sharding",
		SampleRate:  1.0,
	}
}

// Span represents a local topology trace span.
// This intentionally small helper avoids external dependencies and does not
// replace the broader tracing infrastructure owned by x/observability.
type Span struct {
	name       string
	attributes map[string]any
	events     []SpanEvent
	status     SpanStatus
	ctx        context.Context
}

// SpanEvent represents an event within a span
type SpanEvent struct {
	Name       string
	Attributes map[string]any
}

// SpanStatus represents the status of a span
type SpanStatus struct {
	Code    SpanStatusCode
	Message string
}

// SpanStatusCode represents the status code of a span
type SpanStatusCode int

const (
	// SpanStatusUnset is the default status
	SpanStatusUnset SpanStatusCode = iota
	// SpanStatusOK indicates success
	SpanStatusOK
	// SpanStatusError indicates an error
	SpanStatusError
)

// Tracer is a minimal local tracer for sharding topology decisions.
type Tracer struct {
	config TracingConfig
}

// NewTracer creates a new tracer
func NewTracer(config TracingConfig) *Tracer {
	return &Tracer{
		config: config,
	}
}

// StartSpan starts a new span
func (t *Tracer) StartSpan(ctx context.Context, name string) (context.Context, *Span) {
	if !t.config.Enabled {
		return ctx, &Span{ctx: ctx, name: name, attributes: make(map[string]any)}
	}

	span := &Span{
		name:       name,
		attributes: make(map[string]any),
		events:     []SpanEvent{},
		ctx:        ctx,
	}

	// In a real implementation, this would:
	// 1. Generate a span ID and trace ID
	// 2. Store the span in context
	// 3. Connect to tracing backend

	return ctx, span
}

// SetAttribute sets an attribute on the span
func (s *Span) SetAttribute(key string, value any) {
	if s.attributes == nil {
		s.attributes = make(map[string]any)
	}
	s.attributes[key] = value
}

// SetAttributes sets multiple attributes on the span
func (s *Span) SetAttributes(attrs map[string]any) {
	for k, v := range attrs {
		s.SetAttribute(k, v)
	}
}

// AddEvent adds an event to the span
func (s *Span) AddEvent(name string, attributes map[string]any) {
	s.events = append(s.events, SpanEvent{
		Name:       name,
		Attributes: attributes,
	})
}

// SetStatus sets the span status
func (s *Span) SetStatus(code SpanStatusCode, message string) {
	s.status = SpanStatus{
		Code:    code,
		Message: message,
	}
}

// RecordError records an error in the span
func (s *Span) RecordError(err error) {
	if err == nil {
		return
	}

	s.SetStatus(SpanStatusError, "redacted")
	s.AddEvent("error", map[string]any{
		"error.message":  "redacted",
		"error.redacted": true,
		"error.type":     fmt.Sprintf("%T", err),
	})
}

// End is a no-op for this local span. It leaves the context unchanged.
func (s *Span) End() {}

// Context returns the span's context
func (s *Span) Context() context.Context {
	return s.ctx
}

// TracingHelper provides helper methods for creating tracing spans
type TracingHelper struct {
	tracer *Tracer
}

// NewTracingHelper creates a new tracing helper
func NewTracingHelper(config TracingConfig) *TracingHelper {
	return &TracingHelper{
		tracer: NewTracer(config),
	}
}

// TraceQuery traces a database query without recording raw SQL text.
func (h *TracingHelper) TraceQuery(ctx context.Context, query string, args []any) (context.Context, *Span) {
	ctx, span := h.tracer.StartSpan(ctx, "db.query")

	span.SetAttributes(map[string]any{
		"db.system":             "sharding",
		"db.operation":          getQueryOperation(query),
		"db.statement.redacted": true,
	})

	if len(args) > 0 {
		span.SetAttribute("db.args.count", len(args))
	}

	return ctx, span
}

// TraceShardResolve traces shard resolution
func (h *TracingHelper) TraceShardResolve(ctx context.Context, tableName string) (context.Context, *Span) {
	ctx, span := h.tracer.StartSpan(ctx, "shard.resolve")

	span.SetAttributes(map[string]any{
		"shard.table": tableName,
	})

	return ctx, span
}

// TraceSQLRewrite traces SQL rewriting without recording raw SQL text.
func (h *TracingHelper) TraceSQLRewrite(ctx context.Context, originalSQL string) (context.Context, *Span) {
	ctx, span := h.tracer.StartSpan(ctx, "sql.rewrite")

	span.SetAttributes(map[string]any{
		"db.operation":          getQueryOperation(originalSQL),
		"db.statement.redacted": true,
		"sql.rewrite":           true,
	})

	return ctx, span
}

// TraceShardQuery traces a query to a specific shard without recording raw SQL text.
func (h *TracingHelper) TraceShardQuery(ctx context.Context, shardIndex int, query string) (context.Context, *Span) {
	ctx, span := h.tracer.StartSpan(ctx, "shard.query")

	span.SetAttributes(map[string]any{
		"shard.index":           shardIndex,
		"db.operation":          getQueryOperation(query),
		"db.statement.redacted": true,
	})

	return ctx, span
}

// TraceCrossShardQuery traces a cross-shard query
func (h *TracingHelper) TraceCrossShardQuery(ctx context.Context, shardCount int, policy string) (context.Context, *Span) {
	ctx, span := h.tracer.StartSpan(ctx, "shard.cross_query")

	span.SetAttributes(map[string]any{
		"shard.count":  shardCount,
		"shard.policy": policy,
	})

	return ctx, span
}

// getQueryOperation extracts the operation type from a SQL query
func getQueryOperation(query string) string {
	query = strings.TrimSpace(query)
	if query == "" {
		return "UNKNOWN"
	}
	fields := strings.Fields(query)
	if len(fields) == 0 {
		return "UNKNOWN"
	}
	return strings.ToUpper(fields[0])
}

// InstrumentedRouter wraps a Router with tracing and metrics.
type InstrumentedRouter struct {
	router  *Router
	metrics *MetricsTracker
	tracing *TracingHelper
}

// NewInstrumentedRouter creates a new instrumented router.
func NewInstrumentedRouter(router *Router, metricsEnabled bool, tracingConfig TracingConfig) *InstrumentedRouter {
	var metrics *MetricsTracker
	if metricsEnabled {
		metrics = NewMetricsTracker(router.ShardCount())
	}

	return &InstrumentedRouter{
		router:  router,
		metrics: metrics,
		tracing: NewTracingHelper(tracingConfig),
	}
}

// MetricsTracker returns the metrics tracker.
func (ir *InstrumentedRouter) MetricsTracker() *MetricsTracker {
	return ir.metrics
}

// Router returns the underlying router
func (ir *InstrumentedRouter) Router() *Router {
	return ir.router
}
