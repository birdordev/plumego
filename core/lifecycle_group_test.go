package core

import (
	"context"
	"errors"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestLifecycleGroupStopCompletesPassBeforePropagatingFirstPanic(t *testing.T) {
	firstPanic, laterPanic := errors.New("first panic"), errors.New("later panic")
	var stopped []string
	g := NewLifecycleGroup()
	for _, tc := range []struct {
		name    string
		failure error
	}{
		{"first", nil}, {"second", laterPanic}, {"third", firstPanic},
	} {
		attempted := false
		g.Add(tc.name, Component{OnStop: func(context.Context) error {
			stopped = append(stopped, tc.name)
			if !attempted && tc.failure != nil {
				attempted = true
				panic(tc.failure)
			}
			return nil
		}})
	}
	if err := g.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	var recovered any
	func() {
		defer func() { recovered = recover() }()
		if err := g.Stop(t.Context()); err != nil {
			t.Error(err)
		}
	}()
	if recovered != firstPanic || !slices.Equal(stopped, []string{"third", "second", "first"}) {
		t.Fatalf("panic=%v stop order=%v", recovered, stopped)
	}
	if err := g.Stop(t.Context()); err != nil || !slices.Equal(stopped, []string{"third", "second", "first", "third", "second"}) {
		t.Fatalf("retry error=%v stop order=%v", err, stopped)
	}
}

func TestLifecycleGroupStartupPanicRollsBackAndAllowsRetry(t *testing.T) {
	for _, cleanupPanics := range []bool{false, true} {
		var stops []string
		startPanic := errors.New("start panic")
		group := NewLifecycleGroup().
			Add("first", Component{OnStop: func(context.Context) error {
				stops = append(stops, "first")
				return nil
			}}).
			Add("second", Component{
				OnStart: func(context.Context) error { panic(startPanic) },
				OnStop: func(context.Context) error {
					stops = append(stops, "second")
					if len(stops) == 1 {
						if cleanupPanics {
							panic("cleanup panic")
						}
						return errors.New("cleanup failed")
					}
					return nil
				},
			})
		func() {
			defer func() {
				if got := recover(); got != startPanic {
					t.Errorf("panic = %v, want original startup panic", got)
				}
			}()
			_ = group.Start(t.Context())
		}()
		if !slices.Equal(stops, []string{"second", "first"}) {
			t.Fatalf("rollback order = %v", stops)
		}
		if err := group.Stop(t.Context()); err != nil {
			t.Fatal(err)
		}
		if !slices.Equal(stops, []string{"second", "first", "second"}) {
			t.Fatalf("cleanup retry order = %v", stops)
		}
	}
}

// recorder is a Lifecycle that records the order of Start/Stop calls into a
// shared slice and can be configured to fail any phase.
type recorder struct {
	name     string
	log      *[]string
	startErr error
	stopErr  error
}

func (r *recorder) Start(context.Context) error {
	*r.log = append(*r.log, "start:"+r.name)
	return r.startErr
}

func (r *recorder) Stop(context.Context) error {
	*r.log = append(*r.log, "stop:"+r.name)
	return r.stopErr
}

func (r *recorder) Ready(context.Context) error { return nil }

func TestLifecycleGroupStartsInOrderStopsInReverse(t *testing.T) {
	var log []string
	g := NewLifecycleGroup()
	var absent *Component
	g.Add("a", &recorder{name: "a", log: &log}).
		Add("absent", absent).
		Add("b", &recorder{name: "b", log: &log}).
		Add("c", &recorder{name: "c", log: &log})

	if err := g.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err := g.Ready(t.Context()); err != nil {
		t.Fatalf("Ready: %v", err)
	}
	if err := g.Stop(context.Background()); err != nil {
		t.Fatalf("Stop: %v", err)
	}

	want := []string{"start:a", "start:b", "start:c", "stop:c", "stop:b", "stop:a"}
	if len(log) != len(want) {
		t.Fatalf("call log = %v, want %v", log, want)
	}
	for i := range want {
		if log[i] != want[i] {
			t.Fatalf("call log = %v, want %v", log, want)
		}
	}
}

func TestLifecycleGroupRollsBackOnStartFailure(t *testing.T) {
	var log []string
	boom := errors.New("boom")
	g := NewLifecycleGroup()
	g.Add("a", &recorder{name: "a", log: &log}).
		Add("b", &recorder{name: "b", log: &log, startErr: boom}).
		Add("c", &recorder{name: "c", log: &log})

	err := g.Start(context.Background())
	if err == nil || !errors.Is(err, boom) {
		t.Fatalf("Start error = %v, want wrapping %v", err, boom)
	}
	// Clean up b's partial startup before rolling back a.
	want := []string{"start:a", "start:b", "stop:b", "stop:a"}
	if len(log) != len(want) {
		t.Fatalf("call log = %v, want %v", log, want)
	}
	for i := range want {
		if log[i] != want[i] {
			t.Fatalf("call log = %v, want %v", log, want)
		}
	}
	// A subsequent Stop must not re-stop anything (nothing is running).
	log = nil
	if err := g.Stop(context.Background()); err != nil {
		t.Fatalf("Stop after rollback: %v", err)
	}
	if len(log) != 0 {
		t.Fatalf("Stop after rollback ran %v, want no-op", log)
	}
	if err := g.Ready(t.Context()); err == nil {
		t.Fatal("group reported ready after failed startup")
	}
	g.Add("late", Component{})
	if len(g.entries) != 3 {
		t.Fatal("registration reopened after failed startup")
	}
}

func TestLifecycleGroupRetainsFailedRollbackForStop(t *testing.T) {
	var log []string
	startErr := errors.New("partial start")
	firstStopErr := errors.New("first component rollback failed")
	partialStopErr := errors.New("partial component rollback failed")
	first := &recorder{name: "a", log: &log, stopErr: firstStopErr}
	partial := &recorder{name: "c", log: &log, startErr: startErr, stopErr: partialStopErr}
	g := NewLifecycleGroup().Add("a", first).
		Add("b", &recorder{name: "b", log: &log}).Add("c", partial)

	err := g.Start(t.Context())
	if !errors.Is(err, startErr) || !errors.Is(err, firstStopErr) || !errors.Is(err, partialStopErr) {
		t.Fatalf("Start error = %v, want %v, %v, and %v", err, startErr, firstStopErr, partialStopErr)
	}
	if want := []string{"start:a", "start:b", "start:c", "stop:c", "stop:b", "stop:a"}; !slices.Equal(log, want) {
		t.Fatalf("call log = %v, want %v", log, want)
	}

	log = nil
	first.stopErr = nil
	partial.stopErr = nil
	if err := g.Stop(t.Context()); err != nil {
		t.Fatalf("Stop after failed rollback: %v", err)
	}
	if want := []string{"stop:c", "stop:a"}; !slices.Equal(log, want) {
		t.Fatalf("retry log = %v, want %v", log, want)
	}
}

func TestLifecycleGroupStopAggregatesErrors(t *testing.T) {
	var log []string
	e1 := errors.New("e1")
	e2 := errors.New("e2")
	g := NewLifecycleGroup()
	g.Add("a", &recorder{name: "a", log: &log, stopErr: e1}).
		Add("b", &recorder{name: "b", log: &log, stopErr: e2})

	if err := g.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	err := g.Stop(context.Background())
	if err == nil || !errors.Is(err, e1) || !errors.Is(err, e2) {
		t.Fatalf("Stop error = %v, want both e1 and e2", err)
	}
}

func TestLifecycleGroupStopRetriesOnlyFailedComponents(t *testing.T) {
	var log []string
	g := NewLifecycleGroup().
		Add("a", &recorder{name: "a", log: &log}).
		Add("b", Component{OnStop: func(ctx context.Context) error {
			log = append(log, "stop:b")
			return ctx.Err()
		}}).
		Add("c", &recorder{name: "c", log: &log})
	if err := g.Start(t.Context()); err != nil {
		t.Fatalf("Start: %v", err)
	}

	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := g.Stop(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("first Stop error = %v, want context.Canceled", err)
	}
	if err := g.Stop(t.Context()); err != nil {
		t.Fatalf("retry Stop: %v", err)
	}
	if err := g.Stop(t.Context()); err != nil {
		t.Fatalf("Stop after cleanup: %v", err)
	}
	if want := []string{"start:a", "start:c", "stop:c", "stop:b", "stop:a", "stop:b"}; !slices.Equal(log, want) {
		t.Fatalf("call log = %v, want %v", log, want)
	}
}

func TestLifecycleGroupReadyAggregates(t *testing.T) {
	firstErr := errors.New("warming up")
	secondErr := errors.New("dependency unavailable")
	child := NewLifecycleGroup().
		Add("a", Component{OnReady: func(context.Context) error { return firstErr }}).
		Add("b", Component{OnReady: func(context.Context) error { return secondErr }})
	g := NewLifecycleGroup().Add("child", child)
	if err := g.Start(t.Context()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err := g.Ready(t.Context()); !errors.Is(err, firstErr) || !errors.Is(err, secondErr) {
		t.Fatalf("Ready error = %v, want both %v and %v", err, firstErr, secondErr)
	}
}

func TestLifecycleGroupReadyCancellationPreservesProbeErrors(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	probeErr := errors.New("component unavailable")
	var probes []string
	g := NewLifecycleGroup().
		Add("first", Component{OnReady: func(probeCtx context.Context) error {
			probes = append(probes, "first")
			if probeCtx == ctx {
				cancel()
				return probeErr
			}
			return nil
		}}).
		Add("second", Component{OnReady: func(context.Context) error {
			probes = append(probes, "second")
			return nil
		}})
	if err := g.Start(t.Context()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() {
		if err := g.Stop(t.Context()); err != nil {
			t.Errorf("Stop: %v", err)
		}
	})

	if err := g.Ready(ctx); !errors.Is(err, context.Canceled) || !errors.Is(err, probeErr) {
		t.Fatalf("Ready error = %v, want cancellation and %v", err, probeErr)
	}
	if want := []string{"first"}; !slices.Equal(probes, want) {
		t.Fatalf("probes after cancellation = %v, want %v", probes, want)
	}
	if err := g.Ready(ctx); !errors.Is(err, context.Canceled) || len(probes) != 1 {
		t.Fatalf("Ready with canceled context: probes = %v, error = %v", probes, err)
	}
	if err := g.Ready(nil); err != nil {
		t.Fatalf("Ready retry with nil context: %v", err)
	}
	if want := []string{"first", "first", "second"}; !slices.Equal(probes, want) {
		t.Fatalf("probes after retry = %v, want %v", probes, want)
	}
}

func TestLifecycleGroupReadyPanicAllowsAnotherProbeAndStop(t *testing.T) {
	ctx := t.Context()
	probePanic := errors.New("readiness callback panic")
	probes, stops := 0, 0
	g := NewLifecycleGroup().Add("worker", Component{
		OnReady: func(context.Context) error {
			probes++
			if probes == 1 {
				panic(probePanic)
			}
			return nil
		},
		OnStop: func(context.Context) error {
			stops++
			return nil
		},
	})
	if err := g.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	func() {
		defer func() {
			if got := recover(); got != probePanic {
				t.Fatalf("Ready panic = %v, want %v", got, probePanic)
			}
		}()
		_ = g.Ready(ctx)
	}()
	if err := g.Ready(ctx); err != nil {
		t.Fatalf("Ready after panic: %v", err)
	}
	if err := g.Stop(ctx); err != nil || stops != 1 {
		t.Fatalf("Stop after panic: calls=%d error=%v", stops, err)
	}
	if err := g.Ready(ctx); err == nil || probes != 2 {
		t.Fatalf("Ready after Stop: calls=%d error=%v", probes, err)
	}
}

func TestLifecycleGroupReadyOnlyAfterSuccessfulStart(t *testing.T) {
	ctx := t.Context()
	child := NewLifecycleGroup()
	g := NewLifecycleGroup().Add("empty child", child)
	if err := g.Ready(ctx); err == nil {
		t.Fatal("group reported ready before startup")
	}
	if err := g.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	late := Component{OnReady: func(context.Context) error { return errors.New("late component probed") }}
	g.Add("late", late)
	if err := g.Ready(ctx); err != nil {
		t.Fatalf("Ready after successful startup: %v", err)
	}
	if err := g.Stop(ctx); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	g.Add("also late", late)
	if len(g.entries) != 1 {
		t.Fatal("late additions changed the registered components")
	}
	if err := g.Ready(ctx); err == nil {
		t.Fatal("group reported ready after shutdown")
	}
}

func TestLifecycleGroupDoesNotWaitForActiveCallback(t *testing.T) {
	for _, phase := range []string{"start", "stop", "ready"} {
		t.Run(phase, func(t *testing.T) {
			g := NewLifecycleGroup()
			entered := make(chan struct{})
			release := make(chan struct{})
			finished := make(chan error, 1)
			released := false
			defer func() {
				if !released {
					close(release)
				}
			}()
			callback := func(ctx context.Context) error {
				g.Add("reentrant", Component{})
				if err := g.Stop(ctx); err == nil {
					return errors.New("reentrant Stop succeeded")
				}
				if err := g.Ready(ctx); err == nil {
					return errors.New("reentrant Ready succeeded")
				}
				close(entered)
				<-release
				return nil
			}
			component := Component{}
			var run func(context.Context) error
			switch phase {
			case "start":
				component.OnStart, run = callback, g.Start
			case "stop":
				component.OnStop, run = callback, g.Stop
			case "ready":
				component.OnReady, run = callback, g.Ready
			}
			g.Add("worker", component)
			if phase != "start" {
				if err := g.Start(t.Context()); err != nil {
					t.Fatalf("Start: %v", err)
				}
			}

			go func() { finished <- run(t.Context()) }()
			select {
			case <-entered:
			case err := <-finished:
				t.Fatalf("callback returned before blocking: %v", err)
			case <-time.After(2 * time.Second):
				t.Fatal("reentrant calls blocked the callback")
			}

			ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
			defer cancel()
			probe := make(chan error, 1)
			go func() { probe <- g.Ready(ctx) }()
			select {
			case err := <-probe:
				if err == nil {
					t.Fatal("Ready succeeded while a callback was active")
				}
			case <-ctx.Done():
				t.Fatal("Ready blocked on an active callback")
			}
			close(release)
			released = true
			select {
			case err := <-finished:
				if err != nil {
					t.Fatalf("active operation: %v", err)
				}
			case <-ctx.Done():
				t.Fatal("active operation did not finish")
			}
		})
	}
}

func TestLifecycleGroupSecondStartFails(t *testing.T) {
	var log []string
	g := NewLifecycleGroup()
	g.Add("a", &recorder{name: "a", log: &log})
	if err := g.Start(context.Background()); err != nil {
		t.Fatalf("first Start: %v", err)
	}
	if err := g.Start(context.Background()); err == nil {
		t.Fatal("second Start should fail")
	}
}

func TestComponentAdapterOptionalPhases(t *testing.T) {
	var started, stopped bool
	c := Component{
		Name:    "worker",
		OnStart: func(context.Context) error { started = true; return nil },
		OnStop:  func(context.Context) error { stopped = true; return nil },
		// OnReady nil -> always ready.
	}
	ctx := context.Background()
	if err := c.Start(ctx); err != nil || !started {
		t.Fatalf("Start: err=%v started=%v", err, started)
	}
	if err := c.Ready(ctx); err != nil {
		t.Fatalf("Ready with nil OnReady should be nil, got %v", err)
	}
	if err := c.Stop(ctx); err != nil || !stopped {
		t.Fatalf("Stop: err=%v stopped=%v", err, stopped)
	}
}

func TestLifecycleGroupAddNilIsNoop(t *testing.T) {
	g := NewLifecycleGroup()
	g.Add("nil", nil)
	if err := g.Start(context.Background()); err != nil {
		t.Fatalf("Start with only nil component: %v", err)
	}
}

// Compile-time assertions: the group and the adapter satisfy Lifecycle, and a
// group composes into another group.
var (
	_ Lifecycle    = (*LifecycleGroup)(nil)
	_ Lifecycle    = Component{}
	_ Starter      = (*LifecycleGroup)(nil)
	_ Stopper      = (*LifecycleGroup)(nil)
	_ ReadyChecker = (*LifecycleGroup)(nil)
)

func TestLifecycleGroupCanceledStartLeavesGroupIdle(t *testing.T) {
	var events []string
	group := NewLifecycleGroup().Add("first", &recorder{name: "first", log: &events})
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	if err := group.Start(ctx); !errors.Is(err, context.Canceled) {
		t.Errorf("Start error = %v, want canceled", err)
	}
	if len(events) != 0 {
		t.Errorf("callbacks before retry: %v", events)
	}
	if err := group.Start(t.Context()); err != nil {
		t.Fatalf("retry Start: %v", err)
	}
	if err := group.Stop(t.Context()); err != nil {
		t.Fatal(err)
	}
}

func TestLifecycleGroupCancellationRollsBackAttemptedComponents(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	var events []string
	cleanupErr := errors.New("cleanup needs a live context")
	first := &recorder{name: "first", log: &events}
	group := NewLifecycleGroup().Add("first", first).
		Add("second", Component{
			OnStart: func(context.Context) error {
				events = append(events, "start:second")
				cancel()
				return nil
			},
			OnStop: func(ctx context.Context) error {
				events = append(events, "stop:second")
				if ctx.Err() != nil {
					return cleanupErr
				}
				return nil
			},
		}).
		Add("third", &recorder{name: "third", log: &events})

	if err := group.Start(ctx); !errors.Is(err, context.Canceled) || !errors.Is(err, cleanupErr) {
		t.Errorf("Start error = %v, want cancellation and cleanup errors", err)
	}
	want := []string{"start:first", "start:second", "stop:second", "stop:first"}
	if !slices.Equal(events, want) {
		t.Errorf("callbacks = %v, want %v", events, want)
	}
	if err := group.Stop(t.Context()); err != nil {
		t.Fatal(err)
	}
	want = append(want, "stop:second")
	if !slices.Equal(events, want) {
		t.Errorf("callbacks after retry = %v, want %v", events, want)
	}
}

func TestLifecycleGroupStopNormalizesNilContext(t *testing.T) {
	group := NewLifecycleGroup().Add("component", Component{OnStop: func(ctx context.Context) error {
		if ctx == nil {
			t.Error("cleanup received a nil context")
		}
		return nil
	}})
	if err := group.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := group.Stop(nil); err != nil {
		t.Fatal(err)
	}
}

func TestLifecycleGroupRollbackPanicPreservesStartupError(t *testing.T) {
	startErr := errors.New("startup failed")
	var stops []string
	secondAttempts := 0
	g := NewLifecycleGroup().Add("first", Component{OnStop: func(context.Context) error {
		stops = append(stops, "first")
		return nil
	}}).Add("second", Component{
		OnStart: func(context.Context) error { return startErr },
		OnStop: func(context.Context) error {
			stops = append(stops, "second")
			secondAttempts++
			if secondAttempts == 1 {
				panic("rollback failed")
			}
			return nil
		},
	})
	err := g.Start(t.Context())
	if !errors.Is(err, startErr) || err == startErr || !slices.Equal(stops, []string{"second", "first"}) {
		t.Fatalf("start error=%v, rollback=%v", err, stops)
	}
	if err := g.Stop(t.Context()); err != nil || !slices.Equal(stops, []string{"second", "first", "second"}) {
		t.Fatalf("retry error=%v, stops=%v", err, stops)
	}
}

func TestLifecycleGroupRetainsPendingStopAfterGoexit(t *testing.T) {
	var calls []string
	exits := true
	g := NewLifecycleGroup().
		Add("first", Component{OnStop: func(context.Context) error {
			calls = append(calls, "first")
			return nil
		}}).
		Add("second", Component{OnStop: func(context.Context) error {
			calls = append(calls, "second")
			if exits {
				runtime.Goexit()
			}
			return nil
		}}).
		Add("third", Component{OnStop: func(context.Context) error {
			calls = append(calls, "third")
			return nil
		}})
	if err := g.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	exited := make(chan struct{})
	go func() {
		defer close(exited)
		if err := g.Stop(t.Context()); err != nil {
			t.Error(err)
		}
		t.Error("first Stop unexpectedly returned")
	}()
	<-exited
	exits = false
	if err := g.Stop(t.Context()); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(calls, []string{"third", "second", "second", "first"}) {
		t.Fatalf("cleanup order = %v", calls)
	}
}

func TestReadyPanicRestoresGroupBeforeStop(t *testing.T) {
	var order []string
	g := NewLifecycleGroup().
		Add("first", Component{OnStop: func(context.Context) error { order = append(order, "first"); return nil }}).
		Add("second", Component{OnReady: func(context.Context) error { panic("probe") },
			OnStop: func(context.Context) error { order = append(order, "second"); return nil }})
	if err := g.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	func() {
		defer func() {
			if got := recover(); got != "probe" {
				t.Errorf("panic=%v", got)
			}
		}()
		_ = g.Ready(t.Context())
	}()
	if err := g.Stop(t.Context()); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(order, []string{"second", "first"}) || g.Ready(t.Context()) == nil {
		t.Fatalf("cleanup=%v phase=%v", order, g.phase)
	}
}

func TestNestedGroupRetriesOnlyPendingChildCleanup(t *testing.T) {
	var calls []string
	startErr, stopErr := errors.New("start failed"), errors.New("rollback failed")
	failed := &recorder{name: "failed", log: &calls, startErr: startErr, stopErr: stopErr}
	child := NewLifecycleGroup().
		Add("ok", &recorder{name: "ok", log: &calls}).
		Add("failed", failed)
	parent := NewLifecycleGroup().Add("child", child)
	err := parent.Start(t.Context())
	if !errors.Is(err, startErr) || !errors.Is(err, stopErr) {
		t.Fatalf("startup chain = %v", err)
	}
	if want := []string{"start:ok", "start:failed", "stop:failed", "stop:ok", "stop:failed"}; !slices.Equal(calls, want) {
		t.Fatalf("rollback calls=%v, want %v", calls, want)
	}
	failed.stopErr = nil
	calls = nil
	if err := parent.Stop(t.Context()); err != nil || !slices.Equal(calls, []string{"stop:failed"}) {
		t.Fatalf("retry calls=%v err=%v", calls, err)
	}
}

func TestReadyGoexitRestoresRunningPhase(t *testing.T) {
	probes, stopped := 0, 0
	group := NewLifecycleGroup().Add("fixture", Component{
		OnReady: func(context.Context) error {
			probes++
			if probes == 1 {
				runtime.Goexit()
			}
			return nil
		},
		OnStop: func(context.Context) error { stopped++; return nil },
	})
	if err := group.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = group.Ready(t.Context())
		t.Error("Ready returned after Goexit")
	}()
	<-done
	if err := group.Ready(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := group.Stop(t.Context()); err != nil || stopped != 1 || probes != 2 {
		t.Fatalf("Stop=%v checked=%d stopped=%d", err, probes, stopped)
	}
}

type observableCanceledContext struct {
	context.Context
	inspect func()
}

func (c observableCanceledContext) Err() error {
	c.inspect()
	return c.Context.Err()
}

func TestCanceledStartCheckLeavesLifecycleRegistrationOpen(t *testing.T) {
	g := NewLifecycleGroup()
	parent, cancel := context.WithCancel(t.Context())
	cancel()
	var inspected, phaseLocked bool
	ctx := observableCanceledContext{Context: parent, inspect: func() {
		inspected = true
		if g.mu.TryLock() {
			g.mu.Unlock()
		} else {
			phaseLocked = true
		}
	}}
	if err := g.Start(ctx); !errors.Is(err, context.Canceled) || !inspected || !phaseLocked {
		t.Fatalf("Start=%v inspected=%t phaseLocked=%t", err, inspected, phaseLocked)
	}
	var started bool
	g.Add("fixture", Component{OnStart: func(context.Context) error { started = true; return nil }})
	if err := g.Start(t.Context()); err != nil || !started {
		t.Fatalf("next Start=%v ran=%t", err, started)
	}
	if err := g.Stop(t.Context()); err != nil {
		t.Fatal(err)
	}
}

func TestLifecycleReadyOwnsPhaseUntilCallbackReturns(t *testing.T) {
	g := NewLifecycleGroup()
	entered, release := make(chan struct{}), make(chan struct{})
	calls := 0
	g.Add("main", Component{OnReady: func(context.Context) error {
		calls++
		close(entered)
		<-release
		return nil
	}, OnStop: func(context.Context) error { calls++; return nil }})
	if err := g.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- g.Ready(t.Context()) }()
	<-entered
	readyErr, stopErr := g.Ready(t.Context()), g.Stop(t.Context())
	g.Add("late", Component{OnStop: func(context.Context) error { t.Error("late component stopped"); return nil }})
	close(release)
	if err := <-done; err != nil || readyErr == nil || stopErr == nil {
		t.Fatalf("ready=%v overlapping=%v stop=%v", err, readyErr, stopErr)
	}
	if err := g.Stop(t.Context()); err != nil || calls != 2 {
		t.Fatalf("final stop=%v calls=%d", err, calls)
	}
}

func TestStopRetriesOrderedFailedComponentsOnly(t *testing.T) {
	g := NewLifecycleGroup()
	aErr, cErr := errors.New("fixture a"), errors.New("fixture c")
	var stops []string
	for _, tc := range []struct {
		name    string
		failure error
	}{{"a", aErr}, {"b", nil}, {"c", cErr}} {
		failure := tc.failure
		g.Add(tc.name, Component{OnStop: func(context.Context) error { stops = append(stops, tc.name); e := failure; failure = nil; return e }})
	}
	if err := g.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	err := g.Stop(t.Context())
	joined, ok := err.(interface{ Unwrap() []error })
	if !ok || !errors.Is(err, aErr) || !errors.Is(err, cErr) || len(joined.Unwrap()) != 2 ||
		!errors.Is(joined.Unwrap()[0], cErr) || !errors.Is(joined.Unwrap()[1], aErr) {
		t.Fatalf("stop error=%v", err)
	}
	if err := g.Stop(t.Context()); err != nil || !slices.Equal(stops, []string{"c", "b", "a", "c", "a"}) {
		t.Fatalf("retry=%v stops=%v", err, stops)
	}
}

func TestCancellationInLastStartRollsBackAllAcquiredComponents(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	g := NewLifecycleGroup()
	var stops []string
	failure := errors.New("fixture release")
	failed := false
	for _, name := range []string{"a", "b", "c"} {
		g.Add(name, Component{
			OnStart: func(context.Context) error {
				if name == "c" {
					cancel()
				}
				return nil
			},
			OnStop: func(ctx context.Context) error {
				stops = append(stops, name)
				if name == "b" && !failed {
					failed = true
					if !errors.Is(ctx.Err(), context.Canceled) {
						t.Errorf("rollback ctx=%v", ctx.Err())
					}
					return failure
				}
				return nil
			},
		})
	}
	if err := g.Start(ctx); !errors.Is(err, context.Canceled) || !errors.Is(err, failure) {
		t.Fatalf("startup=%v", err)
	}
	if err := g.Stop(t.Context()); err != nil || !slices.Equal(stops, []string{"c", "b", "a", "b"}) {
		t.Fatalf("retry=%v stops=%v", err, stops)
	}
}

func TestNestedReadyPreservesErrorAndProbeOrder(t *testing.T) {
	failure := errors.New("fixture")
	var sequence []string
	var cancel context.CancelFunc
	inner := NewLifecycleGroup().Add("first", Component{OnReady: func(context.Context) error {
		sequence = append(sequence, "first")
		if cancel != nil {
			cancel()
		}
		return failure
	}}).Add("second", Component{OnReady: func(context.Context) error {
		sequence = append(sequence, "second")
		return nil
	}})
	outer := NewLifecycleGroup().Add("nested", inner).Add("outer", Component{OnReady: func(context.Context) error {
		sequence = append(sequence, "outer")
		return nil
	}})
	if err := outer.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = outer.Stop(context.Background()) })
	err := outer.Ready(t.Context())
	if !errors.Is(err, failure) || !strings.Contains(err.Error(), "\"nested\"") ||
		!strings.Contains(err.Error(), "\"first\"") || !slices.Equal(sequence, []string{"first", "second", "outer"}) {
		t.Fatalf("error=%v sequence=%v", err, sequence)
	}
	sequence = nil
	ctx, stop := context.WithCancel(t.Context())
	defer stop()
	cancel = stop
	err = outer.Ready(ctx)
	if !errors.Is(err, failure) || !errors.Is(err, context.Canceled) || !slices.Equal(sequence, []string{"first"}) {
		t.Fatalf("error=%v sequence=%v", err, sequence)
	}
}

func TestReadyGoexitReleasesLifecyclePhase(t *testing.T) {
	group := NewLifecycleGroup()
	stops := 0
	group.Add("worker", Component{
		OnReady: func(context.Context) error { runtime.Goexit(); return nil },
		OnStop:  func(context.Context) error { stops++; return nil },
	})
	if err := group.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		_ = group.Ready(t.Context())
	}()
	<-finished
	if err := group.Stop(t.Context()); err != nil || stops != 1 {
		t.Fatalf("Stop=%v calls=%d", err, stops)
	}
	if err := group.Ready(t.Context()); err == nil {
		t.Fatal("stopped group still probes")
	}
}
