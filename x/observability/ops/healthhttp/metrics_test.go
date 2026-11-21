package healthhttp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http/httptest"
	"runtime"
	"slices"
	"sync/atomic"
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
	tracker.recordComponentCheck("test", health.StatusHealthy, time.Millisecond, false)

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

func TestAcceptanceTrackerRecordsOnlyBatchAttempts(t *testing.T) {
	manager, err := NewManager(Config{})
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	tracker := NewTracker(manager)
	abort := false
	if err := manager.RegisterComponent(functionChecker{name: "fixture", check: func(context.Context) error {
		if abort {
			runtime.Goexit()
		}
		return nil
	}}); err != nil {
		t.Fatal(err)
	}
	initial := tracker.CheckAllComponents(t.Context())
	if !initial.Status.IsReady() || len(initial.Dependencies) != 1 {
		t.Fatalf("initial batch=%+v", initial)
	}
	initial.Dependencies[0] = "caller change"
	if history := tracker.GetHealthHistory(); len(history) != 1 ||
		len(history[0].Components) != 1 || history[0].Components[0] != "fixture" {
		t.Fatalf("batch result changed tracker history: %v", history)
	}
	abort = true
	if status := tracker.CheckAllComponents(t.Context()); status.Status.IsReady() {
		t.Fatalf("aborted batch=%+v", status)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	tracker.CheckAllComponents(ctx)
	stats, ok := tracker.GetComponentMetrics("fixture")
	if !ok || stats.CheckCount != 2 || stats.SuccessCount != 1 || stats.FailureCount != 1 ||
		stats.RecentHistory[1].Status != health.StatusUnhealthy || stats.RecentHistory[1].Success {
		t.Fatalf("component stats=%+v", stats)
	}
}

func TestTrackerOverlappingBatchesRecordTheirOwnComponentOutcome(t *testing.T) {
	manager, err := NewManager(Config{})
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	tracker := NewTracker(manager)
	checker := &aggregateChecker{entered: make(chan struct{}), release: make(chan struct{}), newerError: errors.New("fixture")}
	if err := manager.RegisterComponent(checker); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		tracker.CheckAllComponents(t.Context())
	}()
	<-checker.entered
	newer := tracker.CheckAllComponents(t.Context())
	close(checker.release)
	<-done
	stats, _ := tracker.GetComponentMetrics("db")
	if newer.Status.IsReady() || manager.Readiness().Ready || stats.SuccessCount != 1 || stats.FailureCount != 1 ||
		!stats.RecentHistory[1].Success {
		t.Fatalf("batch=%+v readiness=%+v stats=%+v", newer, manager.Readiness(), stats)
	}
}

func TestAcceptanceTrackerAverageRetainsIntegerPrecision(t *testing.T) {
	tracker := NewTracker(nil)
	for _, suite := range []struct {
		samples []time.Duration
		want    time.Duration
	}{
		{[]time.Duration{math.MaxInt64, math.MaxInt64}, math.MaxInt64},
		{[]time.Duration{math.MaxInt64, 0, math.MaxInt64}, 6148914691236517204},
		{[]time.Duration{0, 3, 8, 9}, 5},
	} {
		for _, sample := range suite.samples {
			tracker.recordComponentCheck("fixture", health.StatusHealthy, sample, false)
		}
		stats, _ := tracker.GetComponentMetrics("fixture")
		if stats.AverageLatency != suite.want || stats.AverageLatency < stats.MinLatency ||
			stats.AverageLatency > stats.MaxLatency || stats.CheckCount != int64(len(suite.samples)) {
			t.Fatalf("samples=%v metrics=%+v", suite.samples, stats)
		}
		tracker.Reset()
	}
}

func TestAcceptanceTrackerMetricsUsesFixedPublicDiagnostic(t *testing.T) {
	manager, err := NewManager(Config{})
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	tracker := NewTracker(manager)
	cause := errors.New("fixture checker unavailable")
	checker := functionChecker{name: "db", check: func(context.Context) error { return cause }}
	if err := manager.RegisterComponent(checker); err != nil {
		t.Fatal(err)
	}
	if err := tracker.CheckComponent(t.Context(), "db"); !errors.Is(err, cause) {
		t.Fatalf("checker error=%v", err)
	}
	tracker.CheckAllComponents(t.Context())
	w := httptest.NewRecorder()
	MetricsHandler(tracker).ServeHTTP(w, httptest.NewRequest("GET", "/metrics", nil))
	var response struct{ Data HealthMetrics }
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil || w.Code != 200 {
		t.Fatalf("metrics status=%d decode=%v", w.Code, err)
	}
	stats := response.Data.ComponentMetrics["db"]
	if stats == nil || stats.FailureCount != 2 || len(stats.RecentHistory) != 2 {
		t.Fatalf("stats=%+v", stats)
	}
	for _, record := range stats.RecentHistory {
		if record.ErrorMessage != "component check failed" || record.Success {
			t.Fatalf("record=%+v", record)
		}
	}
}

func TestAbortedBatchPublishesFailureForHealthReport(t *testing.T) {
	manager, err := NewManager(Config{RetryCount: 1})
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	tracker := NewTracker(manager)
	abort := false
	calls := 0
	if err := tracker.RegisterComponent(functionChecker{name: "sample", check: func(context.Context) error {
		if abort {
			calls++
			if calls == 1 {
				return errors.New("retry fixture")
			}
			runtime.Goexit()
		}
		return nil
	}}); err != nil {
		t.Fatal(err)
	}

	for _, target := range []int{200, 503, 200} {
		abort = target == 503
		batch := tracker.CheckAllComponents(t.Context())
		dst := httptest.NewRecorder()
		HealthReportHandler(tracker).ServeHTTP(dst, httptest.NewRequest("GET", "/fixture", nil))
		var output struct{ Data HealthReport }
		if err := json.Unmarshal(dst.Body.Bytes(), &output); err != nil || dst.Code != target ||
			output.Data.HealthStatus.Status != batch.Status || output.Data.Readiness.Ready != (target == 200) {
			t.Fatalf("want=%d got=%d batch=%+v report=%+v decode=%v", target, dst.Code, batch, output.Data, err)
		}
		c, _ := manager.GetComponentHealth("sample")
		wantAttempts := 1
		if abort {
			wantAttempts = 2
		}
		if c == nil || c.Details["check_attempts"] != wantAttempts || c.Status != batch.Status {
			t.Fatalf("component=%+v attempts=%d", c, wantAttempts)
		}
	}
}

func TestTrackerTrendUsesEachHalfLength(t *testing.T) {
	for _, size := range []int{3, 4, 9, 10} {
		for _, scenario := range []struct {
			direction TrendDirection
			score     float64
			good      func(int) bool
		}{
			{TrendStable, 0, func(int) bool { return true }},
			{TrendStable, 0, func(int) bool { return false }},
			{TrendImproving, 1, func(i int) bool { return i >= size/2 }},
			{TrendDeclining, -1, func(i int) bool { return i < size/2 }},
		} {
			tracker := NewTracker(nil)
			for i := range size {
				state := health.StatusUnhealthy
				if scenario.good(i) {
					state = health.StatusHealthy
				}
				tracker.recordComponentCheck("sample", state, time.Second, false)
			}
			comp, _ := tracker.GetComponentMetrics("sample")
			if trend := comp.HealthTrend; trend.Direction != scenario.direction || trend.TrendScore != scenario.score {
				t.Fatalf("window=%d want=%s/%g got=%+v", size, scenario.direction, scenario.score, trend)
			}
		}
	}
}

func TestManualReadinessAndObservedComponentHealthStayIndependent(t *testing.T) {
	m, err := NewManager(Config{})
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	fail := true
	if err := m.RegisterComponent(functionChecker{name: "sample", check: func(context.Context) error {
		if fail {
			return errors.New("fixture")
		}
		return nil
	}}); err != nil {
		t.Fatal(err)
	}
	tracker := NewTracker(m)
	tracker.CheckAllComponents(t.Context())
	m.MarkReady()
	report := tracker.GenerateReport()
	if !report.Readiness.Ready || report.HealthStatus.Status != health.StatusUnhealthy {
		t.Fatalf("manual ready=%+v", report)
	}
	fail = false
	tracker.CheckAllComponents(t.Context())
	m.MarkNotReady("maintenance")
	report = tracker.GenerateReport()
	if report.Readiness.Ready || report.Readiness.Reason != "maintenance" || report.HealthStatus.Status != health.StatusHealthy {
		t.Fatalf("manual unavailable=%+v", report)
	}
	if batch := tracker.CheckAllComponents(t.Context()); !batch.Status.IsReady() || !m.Readiness().Ready {
		t.Fatal("next check did not own readiness")
	}
}

func TestTrackerDirectMetricsUseOwnAttempt(t *testing.T) {
	for _, olderFails := range []bool{false, true} {
		t.Run(fmt.Sprintf("older_fails_%t", olderFails), func(t *testing.T) {
			m, err := NewManager(Config{})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = m.Close() })
			tracker := NewTracker(m)
			var calls atomic.Int32
			entered, release := make(chan struct{}), make(chan struct{})
			checkErr := errors.New("fixture failure")
			if err := tracker.RegisterComponent(functionChecker{name: "fixture", check: func(context.Context) error {
				if calls.Add(1) == 1 {
					close(entered)
					<-release
					if olderFails {
						return checkErr
					}
					return nil
				}
				if !olderFails {
					return checkErr
				}
				return nil
			}}); err != nil {
				t.Fatal(err)
			}
			done := make(chan error, 1)
			go func() { done <- tracker.CheckComponent(t.Context(), "fixture") }()
			<-entered
			newerErr := tracker.CheckComponent(t.Context(), "fixture")
			close(release)
			olderErr := <-done
			ctx, cancel := context.WithCancel(t.Context())
			cancel()
			canceledErr := tracker.CheckComponent(ctx, "fixture")
			if (newerErr != nil) == olderFails || olderErr == nil || olderErr.Error() != "component check was superseded" ||
				!errors.Is(canceledErr, context.Canceled) {
				t.Fatalf("newer=%v older=%v canceled=%v", newerErr, olderErr, canceledErr)
			}
			want := health.StatusHealthy
			if !olderFails {
				want = health.StatusUnhealthy
			}
			component, _ := tracker.GetComponentHealth("fixture")
			stats, ok := tracker.GetComponentMetrics("fixture")
			if !ok || stats.SuccessCount != 1 || stats.FailureCount != 1 || len(stats.RecentHistory) != 2 ||
				stats.RecentHistory[1].Success == olderFails || stats.LastStatus != stats.RecentHistory[1].Status ||
				stats.LastStatus == want || component.Status != want {
				t.Fatalf("stats=%+v component=%+v", stats, component)
			}
		})
	}
}

func TestTrackerCloseKeepsExecutedDirectAttemptMetrics(t *testing.T) {
	m, err := NewManager(Config{})
	if err != nil {
		t.Fatal(err)
	}
	tracker := NewTracker(m)
	entered, release := make(chan struct{}), make(chan struct{})
	if err := tracker.RegisterComponent(functionChecker{name: "fixture", check: func(context.Context) error {
		close(entered)
		<-release
		return nil
	}}); err != nil {
		t.Fatal(err)
	}
	before, _ := tracker.GetComponentHealth("fixture")
	result := make(chan error, 1)
	go func() { result <- tracker.CheckComponent(t.Context(), "fixture") }()
	<-entered
	closeErr := tracker.Close()
	close(release)
	activeErr := <-result
	lateErr := tracker.CheckComponent(t.Context(), "fixture")
	stats, ok := tracker.GetComponentMetrics("fixture")
	after, hasComponent := tracker.GetComponentHealth("fixture")
	if closeErr != nil || !errors.Is(activeErr, errManagerClosed) || !errors.Is(lateErr, errManagerClosed) ||
		!ok || stats.SuccessCount != 1 || stats.CheckCount != 1 || !hasComponent || after.Timestamp != before.Timestamp || after.Status != before.Status {
		t.Fatalf("close=%v active=%v late=%v metrics=%+v has component=%t", closeErr, activeErr, lateErr, stats, hasComponent)
	}
}

func TestTrackerExternalManagerDirectSnapshotFallback(t *testing.T) {
	fixtureError := errors.New("fixture check failure")
	for _, tc := range []struct {
		state   health.HealthState
		exists  bool
		err     error
		success bool
	}{
		{health.StatusHealthy, true, nil, true},
		{health.StatusHealthy, true, fixtureError, false},
		{health.StatusDegraded, true, nil, false},
		{health.StatusUnhealthy, false, fixtureError, false},
	} {
		manager := componentSnapshotManager{status: &health.ComponentHealth{HealthStatus: health.HealthStatus{Status: tc.state}}, exists: tc.exists, checkErr: tc.err}
		tracker := NewTracker(manager)
		if err := tracker.CheckComponent(t.Context(), "fixture"); err != tc.err {
			t.Fatalf("error=%v want=%v", err, tc.err)
		}
		got, ok := tracker.GetComponentMetrics("fixture")
		if ok != tc.exists || (ok && (got.CheckCount != 1 || len(got.RecentHistory) != 1 || got.RecentHistory[0].Success != tc.success || got.LastStatus != tc.state)) {
			t.Fatalf("fixture=%+v metrics=%+v exists=%t", tc, got, ok)
		}
	}
}

func TestGenerateReportSortsComponentNames(t *testing.T) {
	manager, err := NewManager(Config{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = manager.Close() })
	tracker := NewTracker(manager)
	empty, err := json.Marshal(tracker.GenerateReport())
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(empty, &fields); err != nil {
		t.Fatal(err)
	}
	if string(fields["components"]) != "[]" {
		t.Fatalf("empty components=%s", fields["components"])
	}
	for _, name := range []string{"zeta", "gamma", "alpha", "beta", "delta", "epsilon"} {
		if err := tracker.RegisterComponent(functionChecker{name: name}); err != nil {
			t.Fatal(err)
		}
	}
	for range 20 {
		got := tracker.GenerateReport().Components
		if want := []string{"alpha", "beta", "delta", "epsilon", "gamma", "zeta"}; !slices.Equal(got, want) {
			t.Fatalf("report components=%v, want %v", got, want)
		}
	}
}
