package healthhttp

import (
	"net/http/httptest"
	"testing"
	"time"

	"github.com/birdordev/plumego/health"
)

func TestMetricsHandler(t *testing.T) {
	manager, err := NewManager(Config{})
	if err != nil {
		t.Fatalf("failed to create manager: %v", err)
	}
	tracker := NewTracker(manager)
	tracker.recordComponentCheck("test", &health.ComponentHealth{
		HealthStatus: health.HealthStatus{Status: health.StatusHealthy},
	}, time.Millisecond, nil)

	req := httptest.NewRequest("GET", "/metrics", nil)
	rr := httptest.NewRecorder()
	MetricsHandler(tracker).ServeHTTP(rr, req)

	if rr.Code != 200 {
		t.Fatalf("expected status 200, got %d", rr.Code)
	}
	if contentType := rr.Header().Get("Content-Type"); contentType != "application/json" {
		t.Fatalf("expected content type application/json, got %s", contentType)
	}
	metrics := decodeHealthHTTPData[HealthMetrics](t, rr)
	if metrics.CheckCount != 1 {
		t.Fatalf("expected check count 1, got %d", metrics.CheckCount)
	}
}

func TestHealthReportHandler(t *testing.T) {
	manager, err := NewManager(Config{})
	if err != nil {
		t.Fatalf("failed to create manager: %v", err)
	}
	tracker := NewTracker(manager)

	_ = manager.RegisterComponent(&mockChecker{name: "healthy", healthy: true})
	tracker.CheckAllComponents(t.Context())

	req := httptest.NewRequest("GET", "/health/report", nil)
	rr := httptest.NewRecorder()
	HealthReportHandler(tracker).ServeHTTP(rr, req)

	if rr.Code != 200 && rr.Code != 503 {
		t.Fatalf("expected status 200 or 503, got %d", rr.Code)
	}
	if contentType := rr.Header().Get("Content-Type"); contentType != "application/json" {
		t.Fatalf("expected content type application/json, got %s", contentType)
	}
	report := decodeHealthHTTPData[HealthReport](t, rr)
	if report.HealthStatus.Status == "" {
		t.Fatal("expected health report status")
	}
}
