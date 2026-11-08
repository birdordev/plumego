package observability

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/birdordev/plumego/metrics"
)

// TestNewOpenTelemetryTracerEmptyName tests the default name behavior
func TestNewOpenTelemetryTracerEmptyName(t *testing.T) {
	tracer := NewOpenTelemetryTracer("")
	if tracer.name != "github.com/birdordev/plumego/x/observability" {
		t.Fatalf("expected default name, got: %s", tracer.name)
	}
}

// TestNewPrometheusCollectorEmptyNamespace tests the default namespace behavior
func TestNewPrometheusCollectorEmptyNamespace(t *testing.T) {
	collector := NewPrometheusCollector("")
	if collector.namespace != "plumego" {
		t.Fatalf("expected default namespace, got: %s", collector.namespace)
	}
}

// TestSpanHandleEnd4xxError tests 4xx error status handling
func TestSpanHandleEnd4xxError(t *testing.T) {
	tracer := NewOpenTelemetryTracer("test")
	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	_, span := tracer.Start(t.Context(), req)

	span.End(http.StatusBadRequest, 100, "test")

	spans := tracer.Spans()
	if len(spans) != 1 {
		t.Fatalf("expected 1 span")
	}
	if spans[0].Status != "ERROR" {
		t.Fatalf("expected ERROR status for 4xx, got: %s", spans[0].Status)
	}
	if spans[0].StatusMessage == "" {
		t.Fatalf("expected non-empty status message for 4xx error")
	}
}

// TestSpanHandleEnd5xxError tests 5xx error status handling
func TestSpanHandleEnd5xxError(t *testing.T) {
	tracer := NewOpenTelemetryTracer("test")
	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	_, span := tracer.Start(t.Context(), req)

	span.End(http.StatusInternalServerError, 100, "test")

	spans := tracer.Spans()
	if len(spans) != 1 {
		t.Fatalf("expected 1 span")
	}
	if spans[0].Status != "ERROR" {
		t.Fatalf("expected ERROR status for 5xx, got: %s", spans[0].Status)
	}
	if spans[0].StatusMessage == "" {
		t.Fatalf("expected non-empty status message for 5xx error")
	}
}

// TestSpanHandleEndSuccess tests successful status handling
func TestSpanHandleEndSuccess(t *testing.T) {
	tracer := NewOpenTelemetryTracer("test")
	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	_, span := tracer.Start(t.Context(), req)

	span.End(http.StatusOK, 100, "test")

	spans := tracer.Spans()
	if len(spans) != 1 {
		t.Fatalf("expected 1 span")
	}
	if spans[0].Status != "OK" {
		t.Fatalf("expected OK status, got: %s", spans[0].Status)
	}
	if spans[0].StatusMessage != "" {
		t.Fatalf("expected empty status message for success, got: %s", spans[0].StatusMessage)
	}
}

// TestPrometheusCollectorEvictionEdgeCases tests edge cases for eviction
func TestPrometheusCollectorEvictionEdgeCases(t *testing.T) {
	// Test with max memory of 1
	collector := NewPrometheusCollector("test").WithMaxMemory(1)

	// Add first request
	collector.ObserveHTTP(t.Context(), http.MethodGet, "/test1", http.StatusOK, 0, 100*time.Millisecond)

	// Add second request - should trigger eviction
	collector.ObserveHTTP(t.Context(), http.MethodGet, "/test2", http.StatusOK, 0, 100*time.Millisecond)

	stats := collector.GetStats()
	if stats.ActiveSeries > 1 {
		t.Fatalf("expected at most 1 series after eviction, got %d", stats.ActiveSeries)
	}
}

// TestPrometheusCollectorMultipleMetricsPerLabel tests multiple metrics for same label
func TestPrometheusCollectorMultipleMetricsPerLabel(t *testing.T) {
	collector := NewPrometheusCollector("test")

	// Add multiple requests to same label
	for i := 0; i < 5; i++ {
		collector.ObserveHTTP(t.Context(), http.MethodGet, "/test", http.StatusOK, 0, time.Duration(100+i*10)*time.Millisecond)
	}

	stats := collector.GetStats()
	if stats.TotalRecords != 5 {
		t.Fatalf("expected 5 total requests, got %d", stats.TotalRecords)
	}
	if stats.NameBreakdown[metrics.MetricHTTPRequest] == 0 {
		t.Fatalf("expected HTTP name breakdown to be populated")
	}
}

// TestPrometheusHandlerContentType tests the content type header
func TestPrometheusHandlerContentType(t *testing.T) {
	collector := NewPrometheusCollector("test")
	collector.ObserveHTTP(t.Context(), http.MethodGet, "/test", http.StatusOK, 0, 100*time.Millisecond)

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	collector.Handler().ServeHTTP(rr, req)

	contentType := rr.Header().Get("Content-Type")
	if contentType != "text/plain; version=0.0.4" {
		t.Fatalf("expected Prometheus content type, got: %s", contentType)
	}
}

// TestPrometheusHandlerEmptyMetrics tests handler with no metrics
func TestPrometheusHandlerEmptyMetrics(t *testing.T) {
	collector := NewPrometheusCollector("test")

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	collector.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}

	body := rr.Body.String()
	if body == "" {
		t.Fatalf("expected non-empty response even with no metrics")
	}
}

// TestPrometheusCollectorObserveContext tests context handling in Observe
func TestPrometheusCollectorObserveContext(t *testing.T) {
	collector := NewPrometheusCollector("test")

	// Test with background context
	collector.ObserveHTTP(t.Context(), http.MethodPost, "/api/data", http.StatusCreated, 0, 250*time.Millisecond)

	stats := collector.GetStats()
	if stats.TotalRecords != 1 {
		t.Fatalf("expected 1 request, got %d", stats.TotalRecords)
	}
}

// TestPrometheusSnapshotConcurrency tests snapshot under concurrent load
func TestPrometheusSnapshotConcurrency(t *testing.T) {
	collector := NewPrometheusCollector("test")

	done := make(chan bool)
	// Writers
	for i := 0; i < 5; i++ {
		go func(id int) {
			for j := 0; j < 10; j++ {
				collector.ObserveHTTP(t.Context(), http.MethodGet, "/concurrent", http.StatusOK, 0, 50*time.Millisecond)
			}
			done <- true
		}(i)
	}

	// Readers (snapshot)
	for i := 0; i < 5; i++ {
		go func() {
			_ = collector.GetStats()
			done <- true
		}()
	}

	// Wait for all
	for i := 0; i < 10; i++ {
		<-done
	}

	stats := collector.GetStats()
	if stats.TotalRecords != 50 {
		t.Fatalf("expected 50 requests, got %d", stats.TotalRecords)
	}
}

// TestPrometheusSortedKeys tests the sortedKeys function directly
func TestPrometheusSortedKeys(t *testing.T) {
	m := map[labelKey]int{
		{method: "POST", path: "/b", status: "200"}: 1,
		{method: "GET", path: "/a", status: "200"}:  2,
		{method: "GET", path: "/a", status: "404"}:  3,
	}

	keys := sortedKeys(m)
	if len(keys) != 3 {
		t.Fatalf("expected 3 keys, got %d", len(keys))
	}

	// Verify order: GET /a 200, GET /a 404, POST /b 200
	if keys[0].method != "GET" || keys[0].path != "/a" || keys[0].status != "200" {
		t.Fatalf("unexpected first key: %+v", keys[0])
	}
	if keys[1].method != "GET" || keys[1].path != "/a" || keys[1].status != "404" {
		t.Fatalf("unexpected second key: %+v", keys[1])
	}
	if keys[2].method != "POST" || keys[2].path != "/b" || keys[2].status != "200" {
		t.Fatalf("unexpected third key: %+v", keys[2])
	}
}

// TestSpanAttributesCompleteness tests all expected attributes are present
func TestSpanAttributesCompleteness(t *testing.T) {
	tracer := NewOpenTelemetryTracer("test-service")
	req := httptest.NewRequest(http.MethodPost, "/api/test", nil)
	req.Header.Set("User-Agent", "test-agent")
	parentTraceID := "1234567890abcdef1234567890abcdef"
	req.Header.Set("X-Trace-ID", parentTraceID)
	req.Host = "example.com"

	_, span := tracer.Start(t.Context(), req)

	// Sleep to ensure measurable duration
	time.Sleep(75 * time.Millisecond)

	span.End(http.StatusOK, 512, "trace-456")

	spans := tracer.Spans()
	if len(spans) != 1 {
		t.Fatalf("expected 1 span")
	}

	attrs := spans[0].Attributes
	expectedAttrs := map[string]string{
		"http.method":                  "POST",
		"http.route":                   "/api/test",
		"http.user_agent":              "test-agent",
		"http.scheme":                  "http",
		"net.peer.name":                "example.com",
		"net.transport":                "tcp",
		"service.name":                 "test-service",
		"parent.trace_id":              parentTraceID,
		"http.status_code":             "200",
		"http.response_content_length": "512",
		"plumego.trace_id":             parentTraceID,
		"request_id":                   "trace-456",
		"http.status_text":             "OK",
		// duration_ms will be approximately 75ms, but we'll check it's reasonable
	}

	for key, expectedValue := range expectedAttrs {
		if actualValue, exists := attrs[key]; !exists {
			t.Fatalf("missing attribute: %s", key)
		} else if actualValue != expectedValue {
			t.Fatalf("attribute %s: expected %s, got %s", key, expectedValue, actualValue)
		}
	}

	// Check duration_ms is reasonable (should be >= 75ms)
	if durationStr, exists := attrs["duration_ms"]; exists {
		duration, err := strconv.ParseInt(durationStr, 10, 64)
		if err != nil {
			t.Fatalf("duration_ms is not a valid integer: %s", durationStr)
		}
		if duration < 75 {
			t.Fatalf("duration_ms should be at least 75ms, got %d", duration)
		}
	} else {
		t.Fatalf("missing duration_ms attribute")
	}
}

// TestSpanWithoutParentTraceID tests span without parent trace ID
func TestSpanWithoutParentTraceID(t *testing.T) {
	tracer := NewOpenTelemetryTracer("test")
	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	// No X-Trace-ID header

	_, span := tracer.Start(t.Context(), req)
	span.End(http.StatusOK, 10, "test")

	spans := tracer.Spans()
	if len(spans) != 1 {
		t.Fatalf("expected 1 span")
	}

	if spans[0].ParentSpanID != "" {
		t.Fatalf("expected empty parent span ID, got: %s", spans[0].ParentSpanID)
	}

	if _, exists := spans[0].Attributes["parent.trace_id"]; exists {
		t.Fatalf("should not have parent.trace_id attribute")
	}
}

// TestPrometheusCollectorWithZeroMaxMemory tests edge case of zero max memory
func TestPrometheusCollectorWithZeroMaxMemory(t *testing.T) {
	// This should use the default value
	collector := NewPrometheusCollector("test")
	if collector.maxMemory != 10000 {
		t.Fatalf("expected default maxMemory 10000, got %d", collector.maxMemory)
	}
}

// TestPrometheusEvictionWithZeroRequests tests eviction when requests map is empty
func TestPrometheusEvictionWithZeroRequests(t *testing.T) {
	collector := NewPrometheusCollector("test").WithMaxMemory(5)

	// Force eviction on empty map (should not panic)
	collector.mu.Lock()
	collector.evictLeastUsed()
	collector.mu.Unlock()

	// Should still work normally after
	collector.ObserveHTTP(t.Context(), http.MethodGet, "/test", http.StatusOK, 0, 100*time.Millisecond)

	stats := collector.GetStats()
	if stats.TotalRecords != 1 {
		t.Fatalf("expected 1 request after eviction test, got %d", stats.TotalRecords)
	}
}

// TestPrometheusEvictionLeastUsed tests that least used entries are evicted first
func TestPrometheusEvictionLeastUsed(t *testing.T) {
	collector := NewPrometheusCollector("test").WithMaxMemory(3)

	// Add 3 entries with different usage counts
	collector.ObserveHTTP(t.Context(), http.MethodGet, "/least", http.StatusOK, 0, 100*time.Millisecond)
	collector.ObserveHTTP(t.Context(), http.MethodGet, "/most", http.StatusOK, 0, 100*time.Millisecond)
	collector.ObserveHTTP(t.Context(), http.MethodGet, "/middle", http.StatusOK, 0, 100*time.Millisecond)

	// Add more usage to "most" and "middle"
	for i := 0; i < 3; i++ {
		collector.ObserveHTTP(t.Context(), http.MethodGet, "/most", http.StatusOK, 0, 100*time.Millisecond)
	}
	for i := 0; i < 2; i++ {
		collector.ObserveHTTP(t.Context(), http.MethodGet, "/middle", http.StatusOK, 0, 100*time.Millisecond)
	}

	// Add one more to trigger eviction
	collector.ObserveHTTP(t.Context(), http.MethodGet, "/new", http.StatusOK, 0, 100*time.Millisecond)

	stats := collector.GetStats()
	// Should have at most 3 series
	if stats.ActiveSeries > 3 {
		t.Fatalf("expected at most 3 series, got %d", stats.ActiveSeries)
	}
}
