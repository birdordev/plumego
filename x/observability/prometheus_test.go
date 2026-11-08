package observability

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/birdordev/plumego/metrics"
)

func TestPrometheusCollectorObserveAndHandler(t *testing.T) {
	collector := NewPrometheusCollector("plumego_test")

	collector.ObserveHTTP(t.Context(), http.MethodGet, "/test", http.StatusOK, 0, 125*time.Millisecond)

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	collector.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("unexpected status from metrics handler: %d", rr.Code)
	}
	body := rr.Body.String()

	// Check for counter metric
	if !strings.Contains(body, "plumego_test_http_requests_total") {
		t.Fatalf("expected counter metric in response: %s", body)
	}

	// Check for duration sum metric
	if !strings.Contains(body, "plumego_test_http_request_duration_seconds_sum") {
		t.Fatalf("expected duration sum metric in response: %s", body)
	}

	// Check for duration count metric
	if !strings.Contains(body, "plumego_test_http_request_duration_seconds_count") {
		t.Fatalf("expected duration count metric in response: %s", body)
	}

	// Check for min and max metrics
	if !strings.Contains(body, "plumego_test_http_request_duration_seconds_min") {
		t.Fatalf("expected min metric in response: %s", body)
	}
	if !strings.Contains(body, "plumego_test_http_request_duration_seconds_max") {
		t.Fatalf("expected max metric in response: %s", body)
	}

	// Check for uptime metric
	if !strings.Contains(body, "plumego_test_uptime_seconds") {
		t.Fatalf("expected uptime metric in response: %s", body)
	}

	// Check for total requests metric
	if !strings.Contains(body, "plumego_test_http_requests_total_all") {
		t.Fatalf("expected total requests metric in response: %s", body)
	}
}

func TestPrometheusCollectorMultipleRequests(t *testing.T) {
	collector := NewPrometheusCollector("plumego_test")

	// Add multiple requests with different labels
	requests := []struct {
		method string
		path   string
		status int
	}{
		{http.MethodGet, "/api/users", http.StatusOK},
		{http.MethodPost, "/api/users", http.StatusCreated},
		{http.MethodGet, "/api/posts", http.StatusOK},
		{http.MethodGet, "/api/users", http.StatusInternalServerError},
	}

	for _, req := range requests {
		collector.ObserveHTTP(t.Context(), req.method, req.path, req.status, 0, 100*time.Millisecond)
	}

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	collector.Handler().ServeHTTP(rr, req)

	body := rr.Body.String()

	// Check that all expected metrics are present
	expectedMetrics := []string{
		`plumego_test_http_requests_total{method="GET",path="/api/users",status="200"} 1`,
		`plumego_test_http_requests_total{method="POST",path="/api/users",status="201"} 1`,
		`plumego_test_http_requests_total{method="GET",path="/api/posts",status="200"} 1`,
		`plumego_test_http_requests_total{method="GET",path="/api/users",status="500"} 1`,
	}

	for _, expected := range expectedMetrics {
		if !strings.Contains(body, expected) {
			t.Fatalf("expected metric not found: %s\nBody: %s", expected, body)
		}
	}
}

func TestPrometheusCollectorStats(t *testing.T) {
	collector := NewPrometheusCollector("plumego_test")

	// Add some requests
	for i := 0; i < 5; i++ {
		collector.ObserveHTTP(t.Context(), http.MethodGet, "/test", http.StatusOK, 0, time.Duration(100+i*10)*time.Millisecond)
	}

	stats := collector.GetStats()

	if stats.ActiveSeries != 1 {
		t.Fatalf("expected 1 series, got %d", stats.ActiveSeries)
	}
	if stats.TotalRecords != 5 {
		t.Fatalf("expected 5 total requests, got %d", stats.TotalRecords)
	}
	if stats.NameBreakdown[metrics.MetricHTTPRequest] == 0 {
		t.Fatalf("expected HTTP name breakdown to be populated")
	}
	if stats.StartTime.IsZero() {
		t.Fatalf("expected non-zero start time")
	}
}

func TestPrometheusCollectorClear(t *testing.T) {
	collector := NewPrometheusCollector("plumego_test")

	collector.ObserveHTTP(t.Context(), http.MethodGet, "/test", http.StatusOK, 0, 100*time.Millisecond)

	stats := collector.GetStats()
	if stats.TotalRecords != 1 {
		t.Fatalf("expected 1 request before clear")
	}

	collector.Clear()
	stats = collector.GetStats()
	if stats.TotalRecords != 0 {
		t.Fatalf("expected 0 requests after clear")
	}
}

func TestPrometheusCollectorMaxMemory(t *testing.T) {
	collector := NewPrometheusCollector("plumego_test").WithMaxMemory(3)

	// Add more requests than max memory
	for i := 0; i < 5; i++ {
		collector.ObserveHTTP(t.Context(), http.MethodGet, "/test"+string(rune('A'+i)), http.StatusOK, 0, 100*time.Millisecond)
	}

	stats := collector.GetStats()
	if stats.ActiveSeries > 3 {
		t.Fatalf("expected at most 3 series due to max memory limit, got %d", stats.ActiveSeries)
	}
}

func TestPrometheusCollectorConcurrency(t *testing.T) {
	collector := NewPrometheusCollector("plumego_test")

	done := make(chan bool)
	for i := 0; i < 10; i++ {
		go func() {
			collector.ObserveHTTP(t.Context(), http.MethodGet, "/concurrent", http.StatusOK, 0, 50*time.Millisecond)
			done <- true
		}()
	}

	for i := 0; i < 10; i++ {
		<-done
	}

	stats := collector.GetStats()
	if stats.TotalRecords != 10 {
		t.Fatalf("expected 10 requests, got %d", stats.TotalRecords)
	}
}

func TestPrometheusCollectorMetricsFormat(t *testing.T) {
	collector := NewPrometheusCollector("plumego_test")

	collector.ObserveHTTP(t.Context(), http.MethodPost, "/api/data", http.StatusCreated, 0, 250*time.Millisecond)

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	collector.Handler().ServeHTTP(rr, req)

	body := rr.Body.String()

	// Verify Prometheus format
	if !strings.Contains(body, "# HELP") {
		t.Fatalf("expected HELP comments in Prometheus format")
	}
	if !strings.Contains(body, "# TYPE") {
		t.Fatalf("expected TYPE comments in Prometheus format")
	}
	if !strings.Contains(body, "plumego_test_http_requests_total{method=\"POST\",path=\"/api/data\",status=\"201\"} 1") {
		t.Fatalf("expected properly formatted counter metric")
	}
}

func TestPrometheusCollectorLabelEscaping(t *testing.T) {
	collector := NewPrometheusCollector("test")

	// Attempt metric injection via path containing newline and fake metric
	collector.ObserveHTTP(t.Context(), "GET", "/api\ninjected_metric{x=\"y\"} 999", http.StatusOK, 0, 10*time.Millisecond)
	// Attempt label breakout via quote in path
	collector.ObserveHTTP(t.Context(), "GET", `/api"},{evil="true"}`, http.StatusOK, 0, 10*time.Millisecond)

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	collector.Handler().ServeHTTP(rr, req)
	body := rr.Body.String()

	// Verify newlines are escaped: the label value should contain literal \n (not a real newline)
	// so "injected_metric" stays inside the label value, not on its own line as a fake metric.
	for _, line := range strings.Split(body, "\n") {
		if strings.HasPrefix(line, "injected_metric") {
			t.Fatalf("metric injection via newline: fake metric on its own line:\n%s", line)
		}
	}

	// Verify quotes are escaped: the output should contain backslash-quote sequences
	// which prevent label breakout. In the Prometheus format, \" keeps the quote
	// inside the label value rather than terminating it.
	if !strings.Contains(body, "\\\"") {
		t.Fatalf("expected escaped quotes in label value, got:\n%s", body)
	}

	// The escaped newline (\n as literal characters) should appear in the output
	if !strings.Contains(body, "\\n") {
		t.Fatalf("expected escaped newline in label value, got:\n%s", body)
	}
}

func TestPrometheusCollectorEmptyNamespace(t *testing.T) {
	collector := NewPrometheusCollector("")

	if collector.namespace != "plumego" {
		t.Fatalf("expected default namespace 'plumego', got '%s'", collector.namespace)
	}
}

func TestNewPrometheusExporter_NilCollector_ReturnsError(t *testing.T) {
	exporter, err := NewPrometheusExporter(nil)
	if !errors.Is(err, ErrNilCollector) {
		t.Fatalf("error = %v, want %v", err, ErrNilCollector)
	}
	if exporter != nil {
		t.Fatalf("exporter = %v, want nil", exporter)
	}
}

func TestNewPrometheusExporter_ConstructsExporter(t *testing.T) {
	collector := NewPrometheusCollector("plumego_test")
	exporter, err := NewPrometheusExporter(collector)
	if err != nil {
		t.Fatalf("NewPrometheusExporter returned error: %v", err)
	}
	if exporter == nil {
		t.Fatalf("expected exporter")
	}

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	exporter.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusOK)
	}
}

func TestPrometheusCollectorEviction(t *testing.T) {
	collector := NewPrometheusCollector("plumego_test").WithMaxMemory(5)

	// Fill up to limit
	for i := 0; i < 5; i++ {
		collector.ObserveHTTP(t.Context(), http.MethodGet, "/fill"+string(rune('0'+i)), http.StatusOK, 0, 100*time.Millisecond)
	}

	stats := collector.GetStats()
	if stats.ActiveSeries != 5 {
		t.Fatalf("expected 5 series, got %d", stats.ActiveSeries)
	}

	// Add one more, should trigger eviction
	collector.ObserveHTTP(t.Context(), http.MethodGet, "/new", http.StatusOK, 0, 100*time.Millisecond)

	stats = collector.GetStats()
	// Should be at most 5 (max memory)
	if stats.ActiveSeries > 5 {
		t.Fatalf("expected at most 5 series after eviction, got %d", stats.ActiveSeries)
	}
}

// --- PrometheusExporter.Handler() output ---

func TestPrometheusExporter_Handler_OutputFormat(t *testing.T) {
	collector := NewPrometheusCollector("app")
	collector.ObserveHTTP(t.Context(), http.MethodGet, "/ping", http.StatusOK, 0, 10*time.Millisecond)
	collector.ObserveHTTP(t.Context(), http.MethodPost, "/users", http.StatusCreated, 0, 20*time.Millisecond)

	exporter, err := NewPrometheusExporter(collector)
	if err != nil {
		t.Fatal(err)
	}

	w := httptest.NewRecorder()
	exporter.Handler().ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/metrics", nil))

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	body := w.Body.String()

	for _, want := range []string{
		"app_http_requests_total",
		"app_http_request_duration_seconds_sum",
		"app_http_request_duration_seconds_count",
		"app_http_request_duration_seconds_min",
		"app_http_request_duration_seconds_max",
		"app_uptime_seconds",
		"app_http_requests_total_all 2",
		`method="GET"`,
		`path="/ping"`,
		`status="200"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %q in exporter output", want)
		}
	}
}

func TestPrometheusExporter_Handler_ContentType(t *testing.T) {
	collector := NewPrometheusCollector("ns")
	exporter, err := NewPrometheusExporter(collector)
	if err != nil {
		t.Fatal(err)
	}

	w := httptest.NewRecorder()
	exporter.Handler().ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/metrics", nil))

	ct := w.Header().Get("Content-Type")
	if !strings.HasPrefix(ct, "text/plain") {
		t.Errorf("Content-Type = %q, want text/plain prefix", ct)
	}
}

func TestPrometheusExporter_Handler_EmptyCollector(t *testing.T) {
	collector := NewPrometheusCollector("ns")
	exporter, err := NewPrometheusExporter(collector)
	if err != nil {
		t.Fatal(err)
	}

	w := httptest.NewRecorder()
	exporter.Handler().ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/metrics", nil))

	if w.Code != http.StatusOK {
		t.Fatalf("empty collector status = %d, want 200", w.Code)
	}
	if !strings.Contains(w.Body.String(), "ns_uptime_seconds") {
		t.Error("expected uptime line even for empty collector")
	}
}
