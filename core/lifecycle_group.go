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
//     Stop must allow another attempt after an error.
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
// A group is single-use: Start may be called once and freezes registration.
// Component callbacks are serialized. Operations that overlap a callback
// return without waiting, including calls made by the callback itself.
type LifecycleGroup struct {
	mu      sync.Mutex
	phase   lifecyclePhase
	entries []namedLifecycle
}

type lifecyclePhase uint8

const (
	lifecycleIdle lifecyclePhase = iota
	lifecycleStarting
	lifecycleRunning
	lifecycleProbing
	lifecycleStopping
	lifecycleInactive
)

type namedLifecycle struct {
	name        string
	c           Lifecycle
	pendingStop bool
}

// NewLifecycleGroup returns an empty LifecycleGroup.
func NewLifecycleGroup() *LifecycleGroup {
	return &LifecycleGroup{}
}

// Add registers a component under name. Components start in the order added and
// stop in reverse, so register dependencies before their dependents. Add
// returns the group for chaining. It ignores nil components and additions after
// the first Start begins.
func (g *LifecycleGroup) Add(name string, c Lifecycle) *LifecycleGroup {
	if c == nil {
		return g
	}
	g.mu.Lock()
	if g.phase == lifecycleIdle {
		g.entries = append(g.entries, namedLifecycle{name: name, c: c})
	}
	g.mu.Unlock()
	return g
}

// Start launches components in registration order. A nil context uses
// context.Background. A context canceled before startup leaves the group idle.
// On startup failure or cancellation during startup, Start uses ctx to stop
// attempted components in reverse order and returns startup and rollback errors.
// Stop can retry failed cleanup.
func (g *LifecycleGroup) Start(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	g.mu.Lock()
	if g.phase != lifecycleIdle {
		g.mu.Unlock()
		return errors.New("core: LifecycleGroup already started")
	}
	if err := ctx.Err(); err != nil {
		g.mu.Unlock()
		return err
	}
	g.phase = lifecycleStarting
	g.mu.Unlock()

	finalPhase := lifecycleInactive
	defer func() { g.setPhase(finalPhase) }()

	if err := g.startComponents(ctx); err != nil {
		if rollbackErr := g.stopPending(ctx); rollbackErr != nil {
			return errors.Join(err, rollbackErr)
		}
		return err
	}
	finalPhase = lifecycleRunning
	return nil
}

func (g *LifecycleGroup) startComponents(ctx context.Context) error {
	for i := range g.entries {
		if err := ctx.Err(); err != nil {
			return err
		}
		e := &g.entries[i]
		e.pendingStop = true
		if err := e.c.Start(ctx); err != nil {
			return fmt.Errorf("core: start %q: %w", e.name, err)
		}
	}
	return ctx.Err()
}

// Stop cleans up pending components in reverse registration order and joins
// cleanup errors. A later Stop retries only failed cleanup. If a lifecycle
// operation is already executing callbacks, Stop returns an error for retry.
func (g *LifecycleGroup) Stop(ctx context.Context) error {
	g.mu.Lock()
	switch g.phase {
	case lifecycleIdle:
		g.mu.Unlock()
		return nil
	case lifecycleStarting, lifecycleProbing, lifecycleStopping:
		g.mu.Unlock()
		return errors.New("core: LifecycleGroup operation in progress")
	}
	g.phase = lifecycleStopping
	g.mu.Unlock()
	defer g.setPhase(lifecycleInactive)
	return g.stopPending(ctx)
}

// stopPending runs only during startup rollback or shutdown. Those phases own
// the frozen entries and their cleanup flags exclusively.
func (g *LifecycleGroup) stopPending(ctx context.Context) error {
	var errs []error
	for i := len(g.entries) - 1; i >= 0; i-- {
		e := &g.entries[i]
		if !e.pendingStop {
			continue
		}
		if err := e.c.Stop(ctx); err != nil {
			errs = append(errs, fmt.Errorf("core: stop %q: %w", e.name, err))
			continue
		}
		e.pendingStop = false
	}
	return errors.Join(errs...)
}

// Ready probes components after successful startup. It joins their errors and
// returns promptly if another lifecycle operation is executing callbacks.
func (g *LifecycleGroup) Ready(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	g.mu.Lock()
	if g.phase != lifecycleRunning {
		g.mu.Unlock()
		return errors.New("core: LifecycleGroup is not available for readiness checks")
	}
	g.phase = lifecycleProbing
	g.mu.Unlock()
	defer g.setPhase(lifecycleRunning)

	var errs []error
	for _, e := range g.entries {
		if ctx.Err() != nil {
			break
		}
		if err := e.c.Ready(ctx); err != nil {
			errs = append(errs, fmt.Errorf("core: %q not ready: %w", e.name, err))
		}
	}
	if err := ctx.Err(); err != nil {
		errs = append(errs, err)
	}
	return errors.Join(errs...)
}

func (g *LifecycleGroup) setPhase(phase lifecyclePhase) {
	g.mu.Lock()
	g.phase = phase
	g.mu.Unlock()
}
