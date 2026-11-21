package healthhttp

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"testing/synctest"

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
