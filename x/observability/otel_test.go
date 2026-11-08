package observability

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/birdordev/plumego/contract"
)

func TestOpenTelemetryTracer(t *testing.T) {
	tracer := NewOpenTelemetryTracer("plumego-test")

	req := httptest.NewRequest(http.MethodGet, "/hello", nil)
	ctx, span := tracer.Start(t.Context(), req)
	if ctx == nil || span == nil {
		t.Fatalf("tracer should return context and span")
	}

	// Add a small delay to ensure non-zero duration
	time.Sleep(1 * time.Millisecond)

	span.End(http.StatusOK, 10, "abc123")

	spans := tracer.Spans()
	if len(spans) != 1 {
		t.Fatalf("expected one span, got %d", len(spans))
	}
	if spans[0].Name != "http.request" {
		t.Fatalf("unexpected span name: %s", spans[0].Name)
	}
	if spans[0].Attributes["request_id"] != "abc123" {
		t.Fatalf("request id not recorded: %#v", spans[0].Attributes)
	}
	if spans[0].Attributes["plumego.trace_id"] != spans[0].TraceID {
		t.Fatalf("trace id attribute mismatch: %#v", spans[0].Attributes)
	}
	if !(contract.TraceContext{TraceID: spans[0].TraceID, SpanID: spans[0].SpanID}).Valid() {
		t.Fatalf("span identifiers are not valid trace context fields: trace=%q span=%q", spans[0].TraceID, spans[0].SpanID)
	}
	if spans[0].Status != "OK" {
		t.Fatalf("expected OK status, got: %s", spans[0].Status)
	}
	if spans[0].Duration == 0 {
		t.Fatalf("expected non-zero duration")
	}
}

func TestOpenTelemetryTracerError(t *testing.T) {
	tracer := NewOpenTelemetryTracer("plumego-test")

	req := httptest.NewRequest(http.MethodPost, "/api/error", nil)
	_, span := tracer.Start(t.Context(), req)
	if span == nil {
		t.Fatalf("tracer should return span")
	}

	span.End(http.StatusInternalServerError, 50, "error123")

	spans := tracer.Spans()
	if len(spans) != 1 {
		t.Fatalf("expected one span, got %d", len(spans))
	}
	if spans[0].Status != "ERROR" {
		t.Fatalf("expected ERROR status, got: %s", spans[0].Status)
	}
	if spans[0].StatusMessage == "" {
		t.Fatalf("expected non-empty status message for error")
	}
}

func TestOpenTelemetryTracerWithParent(t *testing.T) {
	tracer := NewOpenTelemetryTracer("plumego-test")

	req := httptest.NewRequest(http.MethodGet, "/hello", nil)
	parentTraceID := "1234567890abcdef1234567890abcdef"
	req.Header.Set("X-Trace-ID", parentTraceID)
	_, span := tracer.Start(t.Context(), req)

	span.End(http.StatusOK, 10, "abc123")

	spans := tracer.Spans()
	if len(spans) != 1 {
		t.Fatalf("expected one span, got %d", len(spans))
	}
	if spans[0].TraceID != parentTraceID {
		t.Fatalf("expected trace ID to be sourced from header, got: %s", spans[0].TraceID)
	}
	if spans[0].Attributes["parent.trace_id"] != parentTraceID {
		t.Fatalf("expected parent trace attribute to be set, got: %s", spans[0].Attributes["parent.trace_id"])
	}
}

func TestOpenTelemetryTracerIgnoresInvalidInboundTraceIDForContext(t *testing.T) {
	tracer := NewOpenTelemetryTracer("plumego-test")

	req := httptest.NewRequest(http.MethodGet, "/hello", nil)
	req.Header.Set("X-Trace-ID", "not-a-valid-trace-id")
	ctx, span := tracer.Start(t.Context(), req)
	if span == nil {
		t.Fatalf("tracer should return span")
	}

	traceCtx, ok := contract.TraceContextFromContext(ctx)
	if !ok || !traceCtx.Valid() {
		t.Fatalf("expected valid generated trace context, got %+v", traceCtx)
	}
	spanCtx, ok := span.(interface {
		TraceID() string
	})
	if !ok {
		t.Fatalf("expected span to expose trace id")
	}
	if spanCtx.TraceID() == "not-a-valid-trace-id" {
		t.Fatalf("invalid inbound trace id should not become propagation trace id")
	}
}

func TestOpenTelemetryTracerStats(t *testing.T) {
	tracer := NewOpenTelemetryTracer("plumego-test")

	// Add multiple spans
	for i := 0; i < 5; i++ {
		req := httptest.NewRequest(http.MethodGet, "/test", nil)
		_, span := tracer.Start(t.Context(), req)

		// Add a small delay to ensure non-zero duration
		time.Sleep(1 * time.Millisecond)

		status := http.StatusOK
		if i == 3 {
			status = http.StatusInternalServerError
		}

		span.End(status, 100, "trace")
	}

	spanStats := tracer.GetSpanStats()
	if spanStats.TotalSpans != 5 {
		t.Fatalf("expected 5 spans, got %d", spanStats.TotalSpans)
	}
	// Check that we have at least one error span (status 500)
	if spanStats.ErrorSpans < 1 {
		t.Fatalf("expected at least 1 error span, got %d", spanStats.ErrorSpans)
	}
	if spanStats.AverageDuration == 0 {
		t.Fatalf("expected non-zero average duration")
	}
}

func TestOpenTelemetryTracerClear(t *testing.T) {
	tracer := NewOpenTelemetryTracer("plumego-test")

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	_, span := tracer.Start(t.Context(), req)
	span.End(http.StatusOK, 10, "test")

	if len(tracer.Spans()) != 1 {
		t.Fatalf("expected 1 span before clear")
	}

	tracer.Clear()
	if len(tracer.Spans()) != 0 {
		t.Fatalf("expected 0 spans after clear")
	}
}

func TestOpenTelemetryTracerConcurrency(t *testing.T) {
	tracer := NewOpenTelemetryTracer("plumego-test")

	// Test concurrent access
	done := make(chan bool)
	for i := 0; i < 10; i++ {
		go func() {
			req := httptest.NewRequest(http.MethodGet, "/test", nil)
			_, span := tracer.Start(t.Context(), req)
			span.End(http.StatusOK, 10, "concurrent")
			done <- true
		}()
	}

	// Wait for all goroutines
	for i := 0; i < 10; i++ {
		<-done
	}

	spans := tracer.Spans()
	if len(spans) != 10 {
		t.Fatalf("expected 10 spans, got %d", len(spans))
	}
}

func TestOpenTelemetryTracerUsesContextTraceID(t *testing.T) {
	tracer := NewOpenTelemetryTracer("plumego-test")

	req := httptest.NewRequest(http.MethodGet, "/hello", nil)
	traceID := "1234567890abcdef1234567890abcdef"
	req.Header.Set("X-Trace-ID", traceID)
	ctx, span := tracer.Start(t.Context(), req)

	spanCtx, ok := span.(interface {
		TraceID() string
		SpanID() string
	})
	if !ok {
		t.Fatalf("expected span to expose context identifiers")
	}
	if spanCtx.TraceID() != traceID {
		t.Fatalf("expected trace id from header, got %s", spanCtx.TraceID())
	}

	traceCtx, ok := contract.TraceContextFromContext(ctx)
	if !ok || traceCtx.TraceID != traceID {
		t.Fatalf("expected contract trace context to be set with traceID %s", traceID)
	}
	if !traceCtx.Valid() {
		t.Fatalf("expected valid trace context, got %+v", traceCtx)
	}
}

func TestSpanIDGeneration(t *testing.T) {
	tracer := NewOpenTelemetryTracer("test")
	spanID1 := tracer.generateSpanID()
	spanID2 := tracer.generateSpanID()

	if spanID1 == spanID2 {
		t.Fatalf("span IDs should be unique")
	}
	if len(spanID1) == 0 || len(spanID2) == 0 {
		t.Fatalf("span IDs should not be empty")
	}
	if !(contract.TraceContext{SpanID: spanID1}).HasSpanID() || !(contract.TraceContext{SpanID: spanID2}).HasSpanID() {
		t.Fatalf("span IDs should be valid 16-character hex values: %q %q", spanID1, spanID2)
	}
}

func TestTraceIDGeneration(t *testing.T) {
	tracer := NewOpenTelemetryTracer("test")
	traceID1 := tracer.generateTraceID()
	traceID2 := tracer.generateTraceID()

	if traceID1 == traceID2 {
		t.Fatalf("trace IDs should be unique")
	}
	if len(traceID1) == 0 || len(traceID2) == 0 {
		t.Fatalf("trace IDs should not be empty")
	}
	if !(contract.TraceContext{TraceID: traceID1}).HasTraceID() || !(contract.TraceContext{TraceID: traceID2}).HasTraceID() {
		t.Fatalf("trace IDs should be valid 32-character hex values: %q %q", traceID1, traceID2)
	}
}

func TestOpenTelemetryTracerDefaultMaxSpans(t *testing.T) {
	tracer := NewOpenTelemetryTracer("plumego-test")
	stats := tracer.GetSpanStats()
	if stats.MaxRetention != defaultMaxSpans {
		t.Fatalf("expected default max spans %d, got %d", defaultMaxSpans, stats.MaxRetention)
	}
}

func TestOpenTelemetryTracerMaxSpansDropOldest(t *testing.T) {
	tracer := NewOpenTelemetryTracer("plumego-test").WithMaxSpans(3)

	for i := 0; i < 5; i++ {
		tracer.record(Span{
			Name:      "http.request",
			SpanID:    "span-" + strconv.Itoa(i),
			TraceID:   "trace-" + strconv.Itoa(i),
			Status:    "OK",
			Timestamp: time.Unix(int64(i), 0),
		})
	}

	spans := tracer.Spans()
	if len(spans) != 3 {
		t.Fatalf("expected 3 spans after retention limit, got %d", len(spans))
	}

	if spans[0].SpanID != "span-2" || spans[1].SpanID != "span-3" || spans[2].SpanID != "span-4" {
		t.Fatalf("expected deterministic drop-oldest behavior, got ids: %s, %s, %s", spans[0].SpanID, spans[1].SpanID, spans[2].SpanID)
	}

	spanStats := tracer.GetSpanStats()
	if spanStats.MaxRetention != 3 {
		t.Fatalf("expected max retention 3, got %d", spanStats.MaxRetention)
	}
	if spanStats.DroppedSpans != 2 {
		t.Fatalf("expected 2 dropped spans, got %d", spanStats.DroppedSpans)
	}

}

func TestOpenTelemetryTracerWithMaxSpansTrimsExisting(t *testing.T) {
	tracer := NewOpenTelemetryTracer("plumego-test")
	for i := 0; i < 4; i++ {
		tracer.record(Span{SpanID: "span-" + strconv.Itoa(i), Status: "OK"})
	}

	tracer.WithMaxSpans(2)

	spans := tracer.Spans()
	if len(spans) != 2 {
		t.Fatalf("expected 2 spans after trim, got %d", len(spans))
	}
	if spans[0].SpanID != "span-2" || spans[1].SpanID != "span-3" {
		t.Fatalf("expected latest spans to be retained, got ids: %s, %s", spans[0].SpanID, spans[1].SpanID)
	}

	stats := tracer.GetSpanStats()
	if stats.DroppedSpans != 2 {
		t.Fatalf("expected dropped spans to include trim operation, got %d", stats.DroppedSpans)
	}
}
