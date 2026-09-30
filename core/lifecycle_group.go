package core

import (
	"context"
	"errors"
	"fmt"
	"sync"
)

// Lifecycle is the canonical contract for a component that owns background
// goroutines (worker pools, schedulers, pollers, watchers, hubs). It unifies
// the divergent Start/Stop/Close/Shutdown/Ready signatures found across the
// extension packages so an application can supervise a heterogeneous set of
// components through one interface.
//
// Contract:
//   - Start spins up the component's goroutines and returns once they are
//     launched. It must be safe to assume Start is called at most once.
//   - Stop drains those goroutines and returns once they have exited (or ctx
//     is cancelled). Stop must be safe to call after a failed or partial Start.
//   - Ready reports whether the component is currently able to serve. It is a
//     point-in-time probe (suitable for readiness checks), not a phase, and may
//     be called repeatedly. A component that is always ready once started
//     returns nil.
//
// Existing types whose method names or signatures differ should adopt the
// interface incrementally; Component bridges the gap without changing their
// public API.
type Lifecycle interface {
	Start(ctx context.Context) error
	Stop(ctx context.Context) error
	Ready(ctx context.Context) error
}

// Starter, Stopper, and ReadyChecker are the individual phases of Lifecycle.
// They let a component implement one phase at a time and let callers depend on
// only the capability they need.
type (
	// Starter launches background work.
	Starter interface {
		Start(ctx context.Context) error
	}
	// Stopper drains background work.
	Stopper interface {
		Stop(ctx context.Context) error
	}
	// ReadyChecker reports point-in-time readiness.
	ReadyChecker interface {
		Ready(ctx context.Context) error
	}
)

// Component adapts a named set of optional lifecycle phases into a Lifecycle.
// It exists so a type whose existing Start/Stop/Ready signatures differ — for
// example a Stop() with no context, or a Close() error — can join a
// LifecycleGroup without altering its public API: wrap the calls in the
// matching field. A nil phase is a no-op that succeeds, which also covers
// components that have no readiness concept or no shutdown step.
type Component struct {
	// Name labels the component in aggregated error messages.
	Name string
	// OnStart, OnStop, and OnReady run for the matching Lifecycle phase. Any of
	// them may be nil.
	OnStart func(ctx context.Context) error
	OnStop  func(ctx context.Context) error
	OnReady func(ctx context.Context) error
}

// Start runs OnStart if set.
func (c Component) Start(ctx context.Context) error {
	if c.OnStart == nil {
		return nil
	}
	return c.OnStart(ctx)
}

// Stop runs OnStop if set.
func (c Component) Stop(ctx context.Context) error {
	if c.OnStop == nil {
		return nil
	}
	return c.OnStop(ctx)
}

// Ready runs OnReady if set.
func (c Component) Ready(ctx context.Context) error {
	if c.OnReady == nil {
		return nil
	}
	return c.OnReady(ctx)
}

// LifecycleGroup supervises an ordered set of Lifecycle components. It starts
// them in registration order, stops them in reverse, and reports readiness only
// when every component is ready. A LifecycleGroup is itself a Lifecycle, so
// groups compose.
//
// The zero value is not usable; construct with NewLifecycleGroup. A group is
// single-use: Start may be called once.
type LifecycleGroup struct {
	mu       sync.Mutex
	entries  []namedLifecycle
	started  int  // number of entries successfully started, for rollback/Stop
	didStart bool // guards against a second Start
}

type namedLifecycle struct {
	name string
	c    Lifecycle
}

// NewLifecycleGroup returns an empty LifecycleGroup.
func NewLifecycleGroup() *LifecycleGroup {
	return &LifecycleGroup{}
}

// Add registers a component under name. Components start in the order added and
// stop in reverse, so register dependencies before their dependents. Add
// returns the group for chaining and is a no-op if c is nil.
func (g *LifecycleGroup) Add(name string, c Lifecycle) *LifecycleGroup {
	if c == nil {
		return g
	}
	g.mu.Lock()
	g.entries = append(g.entries, namedLifecycle{name: name, c: c})
	g.mu.Unlock()
	return g
}

// Start launches every registered component in order. If one fails, the
// components already started are stopped in reverse order (best effort) and the
// originating error is returned, so a failed Start leaves nothing running.
func (g *LifecycleGroup) Start(ctx context.Context) error {
	g.mu.Lock()
	defer g.mu.Unlock()

	if g.didStart {
		return errors.New("core: LifecycleGroup already started")
	}
	g.didStart = true

	for i, e := range g.entries {
		if err := e.c.Start(ctx); err != nil {
			_ = g.stopStartedLocked(ctx, i)
			g.started = 0
			return fmt.Errorf("core: start %q: %w", e.name, err)
		}
		g.started = i + 1
	}
	return nil
}

// Stop drains every started component in reverse order. Every component is
// stopped even if an earlier one errors; the errors are joined and returned.
func (g *LifecycleGroup) Stop(ctx context.Context) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.stopStartedLocked(ctx, g.started)
}

// stopStartedLocked stops entries [0,count) in reverse order, joining errors.
// The caller must hold g.mu.
func (g *LifecycleGroup) stopStartedLocked(ctx context.Context, count int) error {
	var errs []error
	for i := count - 1; i >= 0; i-- {
		e := g.entries[i]
		if err := e.c.Stop(ctx); err != nil {
			errs = append(errs, fmt.Errorf("core: stop %q: %w", e.name, err))
		}
	}
	g.started = 0
	return errors.Join(errs...)
}

// Ready returns nil only when every registered component reports ready. The
// errors from all not-ready components are joined so the caller sees every
// reason at once.
func (g *LifecycleGroup) Ready(ctx context.Context) error {
	g.mu.Lock()
	defer g.mu.Unlock()

	var errs []error
	for _, e := range g.entries {
		if err := e.c.Ready(ctx); err != nil {
			errs = append(errs, fmt.Errorf("core: %q not ready: %w", e.name, err))
		}
	}
	return errors.Join(errs...)
}
