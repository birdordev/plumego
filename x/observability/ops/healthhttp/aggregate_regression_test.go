package healthhttp

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"runtime"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/birdordev/plumego/health"
)

type aggregateChecker struct {
	calls            atomic.Int32
	entered, release chan struct{}
	newerError       error
}

func (c *aggregateChecker) Name() string { return "db" }
func (c *aggregateChecker) Check(context.Context) error {
	if c.calls.Add(1) == 1 {
		close(c.entered)
		<-c.release
		return nil
	}
	return c.newerError
}
func TestAggregateGeneration(t *testing.T) {
	for _, failed := range []bool{false, true} {
		t.Run(map[bool]string{false: "newer-success", true: "newer-failure"}[failed], func(t *testing.T) {
			m, err := NewManager(Config{})
			if err != nil {
				t.Fatal(err)
			}
			defer m.Close()
			c := &aggregateChecker{entered: make(chan struct{}), release: make(chan struct{})}
			if failed {
				c.newerError = errors.New("unavailable")
			}
			if err = m.RegisterComponent(c); err != nil {
				t.Fatal(err)
			}
			done := make(chan struct{})
			go func() { m.CheckAllComponents(context.Background()); close(done) }()
			<-c.entered
			newest := m.CheckAllComponents(context.Background())
			before := m.Readiness()
			close(c.release)
			<-done
			after := m.Readiness()
			if newest.Status.IsReady() != !failed || after.Ready != before.Ready || !after.Timestamp.Equal(before.Timestamp) || after.Reason != before.Reason {
				t.Fatalf("new=%s before=%+v after=%+v", newest.Status, before, after)
			}
			if got, _ := m.GetComponentHealth("db"); (got.Status == health.StatusHealthy) != !failed {
				t.Fatalf("component=%+v", got)
			}
		})
	}
}

func TestAcceptanceAggregateAbnormalChecksHaveFailedOutcomes(t *testing.T) {
	for _, abnormal := range []string{"goexit", "panic"} {
		t.Run(abnormal, func(t *testing.T) {
			manager, err := NewManager(Config{})
			if err != nil {
				t.Fatal(err)
			}
			var abort atomic.Bool
			for i := range 19 {
				if err := manager.RegisterComponent(functionChecker{name: fmt.Sprintf("fixture-%02d", i), check: func(context.Context) error {
					if abort.Load() && i < 8 {
						if abnormal == "goexit" {
							runtime.Goexit()
						}
						panic("internal fixture")
					}
					return nil
				}}); err != nil {
					t.Fatal(err)
				}
			}
			if initial := manager.CheckAllComponents(t.Context()); !initial.Status.IsReady() {
				t.Fatalf("initial=%+v", initial)
			}
			abort.Store(true)
			result := manager.CheckAllComponents(t.Context())
			ready := manager.Readiness()
			if result.Status != health.StatusUnhealthy || ready.Ready || ready.Components["fixture-00"] ||
				!ready.Components["fixture-18"] || strings.Contains(result.Message, "internal fixture") {
				t.Fatalf("result=%+v readiness=%+v", result, ready)
			}
			abort.Store(false)
			if next := manager.CheckAllComponents(t.Context()); !next.Status.IsReady() || !manager.Readiness().Ready {
				t.Fatalf("recovery=%+v", next)
			}
		})
	}
}

func TestAcceptanceAggregateWorkerCountAndCancellation(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		manager, err := NewManager(Config{})
		if err != nil {
			t.Fatal(err)
		}
		const components = 128
		var calls atomic.Int32
		entered := make(chan struct{}, components)
		for i := range components {
			if err := manager.RegisterComponent(functionChecker{name: fmt.Sprint(i), check: func(ctx context.Context) error {
				calls.Add(1)
				entered <- struct{}{}
				<-ctx.Done()
				return ctx.Err()
			}}); err != nil {
				t.Fatal(err)
			}
		}
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		base := runtime.NumGoroutine()
		done := make(chan health.HealthStatus, 1)
		go func() { done <- manager.CheckAllComponents(ctx) }()
		for range optimalConcurrency(components) {
			<-entered
		}
		synctest.Wait()
		workers := runtime.NumGoroutine() - base
		if workers > optimalConcurrency(components)+8 {
			t.Errorf("goroutines created=%d", workers)
		}
		cancel()
		if result := <-done; result.Status.IsReady() || calls.Load() != int32(optimalConcurrency(components)) {
			t.Fatalf("result=%+v checks=%d", result, calls.Load())
		}
	})
}

func BenchmarkAggregateWorkerBound(b *testing.B) {
	for _, size := range []int{8, 128, 512} {
		b.Run(fmt.Sprint(size), func(b *testing.B) {
			manager, err := NewManager(Config{})
			if err != nil {
				b.Fatal(err)
			}
			for i := range size {
				if err := manager.RegisterComponent(functionChecker{name: fmt.Sprint(i), check: func(context.Context) error { return nil }}); err != nil {
					b.Fatal(err)
				}
			}
			b.ReportAllocs()
			for b.Loop() {
				if got := manager.CheckAllComponents(b.Context()); !got.Status.IsReady() || len(got.Dependencies) != size {
					b.Fatalf("incomplete batch: status=%s count=%d", got.Status, len(got.Dependencies))
				}
			}
		})
	}
}

func TestAbortedComponentCannotReplaceNewerRegistrationOrCheck(t *testing.T) {
	for _, mode := range []string{"replace", "remove", "newer", "manual"} {
		t.Run(mode, func(t *testing.T) {
			m, err := NewManager(Config{})
			if err != nil {
				t.Fatal(err)
			}
			defer m.Close()
			entered, release := make(chan struct{}), make(chan struct{})
			var count atomic.Int32
			if err := m.RegisterComponent(functionChecker{name: "sample", check: func(context.Context) error {
				if count.Add(1) == 1 {
					close(entered)
					<-release
					panic("fixture")
				}
				return nil
			}}); err != nil {
				t.Fatal(err)
			}
			finished := make(chan health.HealthStatus, 1)
			go func() { finished <- m.CheckAllComponents(t.Context()) }()
			<-entered
			var changeErr error
			switch mode {
			case "replace":
				changeErr = m.RegisterComponent(functionChecker{name: "sample", check: func(context.Context) error { return nil }})
				if changeErr == nil {
					changeErr = m.CheckComponent(t.Context(), "sample")
				}
			case "remove":
				changeErr = m.UnregisterComponent("sample")
			case "newer":
				changeErr = m.CheckComponent(t.Context(), "sample")
			case "manual":
				m.MarkReady()
			}
			close(release)
			<-finished
			if changeErr != nil {
				t.Fatal(changeErr)
			}
			got, ok := m.GetComponentHealth("sample")
			switch mode {
			case "remove":
				if ok {
					t.Fatalf("removed component=%+v", got)
				}
			case "manual":
				if !m.Readiness().Ready || got.Status != health.StatusUnhealthy {
					t.Fatalf("manual readiness=%+v health=%+v", m.Readiness(), got)
				}
			default:
				if !ok || got.Status != health.StatusHealthy || !m.Readiness().Components["sample"] {
					t.Fatalf("health=%+v readiness=%+v", got, m.Readiness())
				}
			}
		})
	}
}

func TestBatchReadinessUsesCancellationReasonWithoutNewSample(t *testing.T) {
	m, err := NewManager(Config{})
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	if got := m.CheckAllComponents(t.Context()); !got.Status.IsReady() || !m.Readiness().Ready {
		t.Fatal("empty batch is not ready")
	}
	for _, reason := range []error{context.Canceled, context.DeadlineExceeded} {
		var ctx context.Context
		var cancel context.CancelFunc
		if reason == context.Canceled {
			ctx, cancel = context.WithCancel(t.Context())
			cancel()
		} else {
			ctx, cancel = context.WithDeadline(t.Context(), time.Now().Add(-time.Second))
			defer cancel()
		}
		got := m.CheckAllComponents(ctx)
		if ready := m.Readiness(); got.Message != reason.Error() || ready.Ready || ready.Reason != reason.Error() {
			t.Fatalf("reason=%v result=%+v readiness=%+v", reason, got, ready)
		}
	}
	fail := true
	if err := m.RegisterComponent(functionChecker{name: "sample", check: func(context.Context) error {
		if fail {
			return errors.New("fixture")
		}
		return nil
	}}); err != nil {
		t.Fatal(err)
	}
	m.CheckAllComponents(t.Context())
	if ready := m.Readiness(); ready.Reason != "components failed: [sample]" {
		t.Fatalf("failure readiness=%+v", ready)
	}
	fail = false
	m.CheckAllComponents(t.Context())
	before, _ := m.GetComponentHealth("sample")
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	m.CheckAllComponents(ctx)
	after, _ := m.GetComponentHealth("sample")
	if !after.Timestamp.Equal(before.Timestamp) || after.Status != health.StatusHealthy || m.Readiness().Components["sample"] {
		t.Fatalf("before=%+v after=%+v readiness=%+v", before, after, m.Readiness())
	}
}

func TestPreCanceledDirectCheckPreservesActivePublication(t *testing.T) {
	m, err := NewManager(Config{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = m.Close() })
	var calls atomic.Int32
	entered, release := make(chan struct{}), make(chan struct{})
	if err := m.RegisterComponent(functionChecker{name: "fixture", check: func(context.Context) error {
		if calls.Add(1) == 2 {
			close(entered)
			<-release
		}
		return nil
	}}); err != nil {
		t.Fatal(err)
	}
	if err := m.CheckComponent(t.Context(), "fixture"); err != nil {
		t.Fatal(err)
	}
	before, _ := m.GetComponentHealth("fixture")
	finished := make(chan error, 1)
	go func() { finished <- m.CheckComponent(t.Context(), "fixture") }()
	<-entered
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	cancelErr := m.CheckComponent(ctx, "fixture")
	during, _ := m.GetComponentHealth("fixture")
	close(release)
	activeErr := <-finished
	after, _ := m.GetComponentHealth("fixture")
	if !errors.Is(cancelErr, context.Canceled) || activeErr != nil || calls.Load() != 2 {
		t.Fatalf("canceled=%v active=%v attempts=%d", cancelErr, activeErr, calls.Load())
	}
	if during.Status != health.StatusHealthy || during.Timestamp != before.Timestamp ||
		after.Status != health.StatusHealthy || after.Details["check_attempts"] != 1 {
		t.Fatalf("check snapshots: before=%+v during=%+v after=%+v", before, during, after)
	}
}

func TestHealthReportDistinguishesEmptyAndUncheckedComponent(t *testing.T) {
	m, err := NewManager(Config{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = m.Close() })
	tracker := NewTracker(m)
	handler := HealthReportHandler(tracker)
	assert := func(code int, state health.HealthState, names []string, attempts int64) {
		t.Helper()
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/report", nil))
		report := decodeHealthHTTPData[HealthReport](t, rec)
		if rec.Code != code || report.HealthStatus.Status != state || !slices.Equal(report.Components, names) ||
			report.Metrics.CheckCount != attempts {
			t.Fatalf("status=%d health=%s components=%v checks=%d", rec.Code, report.HealthStatus.Status, report.Components, report.Metrics.CheckCount)
		}
	}
	assert(200, health.StatusHealthy, nil, 0)
	if err := m.RegisterComponent(functionChecker{name: "fixture", check: func(context.Context) error { return nil }}); err != nil {
		t.Fatal(err)
	}
	assert(503, health.StatusUnhealthy, []string{"fixture"}, 0)
	if err := tracker.CheckComponent(t.Context(), "fixture"); err != nil {
		t.Fatal(err)
	}
	assert(200, health.StatusHealthy, []string{"fixture"}, 1)
}

type batchErrorContext struct {
	context.Context
	err func() error
}

func (c batchErrorContext) Err() error { return c.err() }

func TestBatchPublicationContextCanReadManagerConfig(t *testing.T) {
	m, err := NewManager(Config{})
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	owner := m.(*manager)
	reads := 0
	ctx := batchErrorContext{Context: t.Context(), err: func() error {
		if !owner.mu.TryRLock() {
			t.Error("batch consulted the context with a manager lock held")
			return nil
		}
		owner.mu.RUnlock()
		_ = m.GetConfig()
		reads++
		return nil
	}}
	result := m.CheckAllComponents(ctx)
	if reads == 0 || !result.Status.IsReady() || !m.Readiness().Ready {
		t.Fatalf("reads=%d result=%+v readiness=%+v", reads, result, m.Readiness())
	}
}

func TestBatchPublicationRevalidatesAfterRefreshingCancellation(t *testing.T) {
	for _, transition := range []string{"none", "newer batch", "manual", "closed"} {
		t.Run(transition, func(t *testing.T) {
			m, err := NewManager(Config{})
			if err != nil {
				t.Fatal(err)
			}
			defer m.Close()
			base, cancel := context.WithCancel(t.Context())
			defer cancel()
			calls := 0
			ctx := batchErrorContext{Context: base, err: func() error {
				calls++
				if calls == 1 {
					sampled := base.Err()
					cancel()
					return sampled
				}
				switch transition {
				case "newer batch":
					m.CheckAllComponents(t.Context())
				case "manual":
					m.MarkNotReady("manual fixture")
				case "closed":
					_ = m.Close()
				}
				return base.Err()
			}}
			got := m.CheckAllComponents(ctx)
			ready := m.Readiness()
			if calls != 2 || got.Status != health.StatusUnhealthy {
				t.Fatalf("samples=%d status=%+v", calls, got)
			}
			switch transition {
			case "none":
				if got.Message != context.Canceled.Error() || ready.Reason != context.Canceled.Error() {
					t.Fatalf("status=%+v ready=%+v", got, ready)
				}
			case "newer batch":
				if got.Message != "aggregate check was superseded" || !ready.Ready {
					t.Fatalf("status=%+v ready=%+v", got, ready)
				}
			case "manual":
				if got.Message != "aggregate check was superseded" || ready.Ready || ready.Reason != "manual fixture" {
					t.Fatalf("status=%+v ready=%+v", got, ready)
				}
			case "closed":
				if got.Message != errManagerClosed.Error() || ready.Ready || ready.Reason != errManagerClosed.Error() {
					t.Fatalf("status=%+v ready=%+v", got, ready)
				}
			}
		})
	}
}

func TestOldBatchFailureKeepsReplacementReady(t *testing.T) {
	m, err := NewManager(Config{})
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	entered, release := make(chan struct{}), make(chan struct{})
	if err := m.RegisterComponent(functionChecker{name: "sample", check: func(context.Context) error { close(entered); <-release; return errors.New("old") }}); err != nil {
		t.Fatal(err)
	}
	m.MarkReady()

	done := make(chan health.HealthStatus, 1)
	go func() { done <- m.CheckAllComponents(t.Context()) }()
	<-entered
	replaceErr := m.RegisterComponent(functionChecker{name: "sample", check: func(context.Context) error { return nil }})
	checkErr := m.CheckComponent(t.Context(), "sample")
	close(release)
	batch := <-done
	if replaceErr != nil || checkErr != nil {
		t.Fatal(replaceErr, checkErr)
	}
	c, _ := m.GetComponentHealth("sample")
	ready := m.Readiness()
	if c.Status != health.StatusHealthy || !ready.Ready || !ready.Components["sample"] {
		t.Errorf("batch=%s/%q current=%s ready=%t components=%v", batch.Status, batch.Message, c.Status, ready.Ready, ready.Components)
	}
}
