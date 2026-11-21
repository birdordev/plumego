package healthhttp

import (
	"context"
	"errors"
	"net/http/httptest"
	"testing"
	"testing/synctest"
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

func TestTrackerResetPreservesManagerReadiness(t *testing.T) {
	manager, err := NewManager(Config{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = manager.Close() })
	tracker := NewTracker(manager)
	const name = "healthy"
	if err := tracker.RegisterComponent(&mockChecker{name: name, healthy: true}); err != nil {
		t.Fatal(err)
	}
	tracker.MarkReady()
	tracker.CheckAllComponents(t.Context())
	if !tracker.Readiness().Ready || tracker.GetMetrics().CheckCount == 0 || len(tracker.GetHealthHistory()) == 0 {
		t.Fatal("expected ready manager and populated tracker before Reset")
	}
	tracker.Reset()
	if got := tracker.GetMetrics(); got.CheckCount != 0 || len(got.ComponentMetrics) != 0 || len(tracker.GetHealthHistory()) != 0 {
		t.Errorf("Reset left metrics or history: %+v", got)
	}
	if _, ok := tracker.GetComponentHealth(name); !ok || !tracker.Readiness().Ready {
		t.Error("Reset changed the wrapped manager's component or readiness state")
	}
	tracker.CheckAllComponents(t.Context())
	if got := tracker.GetMetrics().CheckCount; got != 1 || len(tracker.GetHealthHistory()) == 0 {
		t.Errorf("new check count = %d, history = %d", got, len(tracker.GetHealthHistory()))
	}
}

func TestTrackerSingleCheckRecordsOwnedHistory(t *testing.T) {
	manager, err := NewManager(Config{})
	if err != nil {
		t.Fatal(err)
	}
	tracker := NewTracker(manager)
	t.Cleanup(func() { _ = tracker.Close() })
	sentinel := errors.New("test component unavailable")
	var cause error
	if err := tracker.RegisterComponent(functionChecker{"db", func(context.Context) error { return cause }}); err != nil {
		t.Fatal(err)
	}
	for _, want := range []error{nil, sentinel} {
		cause = want
		if err := tracker.CheckComponent(t.Context(), "db"); !errors.Is(err, want) {
			t.Errorf("check = %v, want %v", err, want)
		}
	}
	if err := tracker.CheckComponent(t.Context(), "missing"); err == nil {
		t.Error("unknown component was accepted")
	}
	if _, ok := tracker.GetComponentMetrics("missing"); ok {
		t.Error("unknown component has metrics")
	}
	got, ok := tracker.GetComponentMetrics("db")
	if !ok || got.CheckCount != 2 || got.SuccessCount != 1 || got.FailureCount != 1 ||
		len(got.RecentHistory) != 2 || !got.RecentHistory[0].Success || got.RecentHistory[1].Success {
		t.Fatalf("component metrics = %+v", got)
	}
	got.RecentHistory[0].Success = false
	got.CheckCount = 0
	again, ok := tracker.GetComponentMetrics("db")
	if !ok || again.CheckCount != 2 || len(again.RecentHistory) != 2 || !again.RecentHistory[0].Success {
		t.Errorf("history was modified through returned metrics: %+v", again)
	}
}

func TestTrackerResetRestartsUptimeAndSuccessRate(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		m, err := NewManager(Config{})
		if err != nil {
			t.Fatal(err)
		}
		tracker := NewTracker(m)
		defer func() { _ = tracker.Close() }()
		if got := tracker.GetSuccessRate(); got != 0 {
			t.Errorf("empty rate=%v", got)
		}
		sentinel := errors.New("fixture unhealthy")
		var cause error
		if err := tracker.RegisterComponent(functionChecker{"sample", func(context.Context) error { return cause }}); err != nil {
			t.Fatal(err)
		}
		for _, want := range []error{nil, sentinel} {
			cause = want
			if err := tracker.CheckComponent(t.Context(), "sample"); !errors.Is(err, want) {
				t.Errorf("CheckComponent=%v, want %v", err, want)
			}
		}
		time.Sleep(3 * time.Second)
		if got := tracker.GetUptime(); got != 3*time.Second {
			t.Errorf("initial uptime=%v", got)
		}
		if got := tracker.GetSuccessRate(); got != 50 {
			t.Errorf("success rate=%v, want 50", got)
		}
		tracker.Reset()
		time.Sleep(time.Second)
		if got := tracker.GetUptime(); got != time.Second {
			t.Errorf("uptime after Reset=%v", got)
		}
		if got := tracker.GetSuccessRate(); got != 0 {
			t.Errorf("rate after Reset=%v", got)
		}
	})
}

func TestTrackerForwardsManualReadinessAndOverallHealth(t *testing.T) {
	m, err := NewManager(Config{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = m.Close() }()
	tracker := NewTracker(m)
	m.MarkReady()
	if !tracker.Readiness().Ready {
		t.Fatal("expected initial readiness")
	}
	tracker.MarkNotReady("fixture maintenance")
	got, want := tracker.Readiness(), m.Readiness()
	if got.Ready || want.Ready || got.Reason != "fixture maintenance" || got.Reason != want.Reason {
		t.Errorf("readiness tracker=%+v manager=%+v", got, want)
	}
	if actual, expected := tracker.GetOverallHealth(), m.GetOverallHealth(); actual.Status != expected.Status || actual.Message != expected.Message {
		t.Errorf("overall health tracker=%+v manager=%+v", actual, expected)
	}
	empty := NewTracker(nil)
	empty.MarkNotReady("unused")
	if got := empty.GetOverallHealth(); got.Status != health.StatusUnhealthy || got.Message != "health manager is nil" || got.Timestamp.IsZero() {
		t.Errorf("nil manager health=%+v", got)
	}
	if empty.Readiness().Ready {
		t.Error("nil manager became ready")
	}
}
