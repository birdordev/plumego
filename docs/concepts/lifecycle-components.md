# Lifecycle for goroutine-managing components

Many extensions own background goroutines — worker pools, schedulers, pollers,
config watchers, health checkers, WebSocket hubs, message dispatchers. Each grew
its own start/stop vocabulary: `Start()`, `Start(ctx)`, `Start(ctx) error`,
`Stop()`, `Stop(ctx) error`, `Close() error`, `Shutdown(ctx) error`,
`StartBackgroundTasks(ctx)`, with readiness expressed ad hoc as `IsRunning()`,
`IsHealthy()`, or nothing at all. An application wiring several of them together
had no single way to start, stop, or probe them.

`core` defines one canonical contract for these components and a supervisor that
runs a set of them in order.

## The contract

```go
type Lifecycle interface {
    Start(ctx context.Context) error // launch background goroutines
    Stop(ctx context.Context) error  // drain them; safe after a partial Start
    Ready(ctx context.Context) error // point-in-time readiness probe
}
```

- **Start** returns once the goroutines are launched (not once they are "warm").
  Assume it is called at most once.
- **Stop** returns once the goroutines have exited or `ctx` is cancelled. It must
  be safe to call after a failed or partial `Start`.
- **Ready** is a *probe*, not a phase: it may be called repeatedly (it backs
  readiness endpoints). A component that is always ready once started returns
  `nil`.

The three phases are also available as single-method interfaces — `core.Starter`,
`core.Stopper`, `core.ReadyChecker` — so a caller can depend on just the
capability it needs and a component can grow into the full contract one method at
a time.

## Supervising a set: `LifecycleGroup`

`core.LifecycleGroup` runs an ordered set of components. It starts them in
registration order, stops them in **reverse** order, and is ready after a
successful start when **every** member is ready. A `LifecycleGroup` is itself a
`Lifecycle`, so groups nest.

```go
g := core.NewLifecycleGroup()
g.Add("message-bus", bus).      // started first, stopped last
  Add("indexer", indexer).
  Add("scheduler", scheduler)   // started last, stopped first

if err := g.Start(ctx); err != nil {
    return err // includes any rollback errors
}
defer g.Stop(context.Background())

// in a readiness handler:
if err := g.Ready(r.Context()); err != nil {
    // 503 — err names every not-ready component
}
```

Guarantees:

- **Ordered start with rollback.** A failed `Start` attempts to stop every
  component whose startup was attempted, including the failing component, in
  reverse order using the startup context. It returns any rollback errors with
  the startup error.
- **Reverse stop with retry.** `Stop` attempts all pending cleanups in reverse
  order and joins their errors. Call it again with a live context to retry
  failed cleanup, including cleanup left pending by startup rollback.
- **Readiness after startup.** `Ready` joins component probe errors after a
  successful start. It returns an error before startup, during lifecycle
  callbacks, after failed startup, and once shutdown begins.
- **Frozen registration and serialized callbacks.** `Add` ignores calls made
  after the first `Start` begins. An overlapping or reentrant `Start`, `Stop`,
  or `Ready` returns an error immediately. If `Stop` overlaps an active
  callback, retry it after that operation finishes.

Register dependencies before their dependents: a message bus before the workers
that publish to it, so startup brings the bus up first and shutdown takes it down
last.

## Adopting without breaking existing APIs

The existing components keep their current method names and signatures. To put
one under a `LifecycleGroup`, wrap its calls in a `core.Component`, whose phases
are all optional (a `nil` phase is a no-op that succeeds):

```go
type Component struct {
    Name    string
    OnStart func(ctx context.Context) error
    OnStop  func(ctx context.Context) error
    OnReady func(ctx context.Context) error
}
```

This bridges every signature shape in the codebase today without changing the
wrapped type:

| Existing shape (example) | Wrapped as `core.Component` |
|---|---|
| `Start(ctx) error` / `Stop(ctx) error` (`x/messaging.Service`, `x/ai/distributed.Worker`) | `OnStart: svc.Start, OnStop: svc.Stop` |
| `Start(ctx)` no error (`x/messaging/mq.Worker`) | `OnStart: func(ctx) error { w.Start(ctx); return nil }` |
| `Start()` no ctx/error (`x/messaging/scheduler.Scheduler`) | `OnStart: func(context.Context) error { s.Start(); return nil }` |
| `Stop()` no ctx/error (`x/websocket.Hub`, `x/gateway` health checker) | `OnStop: func(context.Context) error { h.Stop(); return nil }` |
| `Close() error` (`x/data/cache/distributed`, IPC servers) | `OnStop: func(context.Context) error { return c.Close() }` |
| `Shutdown(ctx) error` (`x/websocket.Hub`, core `App`) | `OnStop: hub.Shutdown` |
| readiness via `IsHealthy()` / `IsRunning()` | `OnReady: func(context.Context) error { if !c.IsHealthy() { return errNotReady }; return nil }` |

New components should implement `core.Lifecycle` directly; existing ones can be
migrated to native implementations over time, with the adapter as the bridge in
the meantime. Adoption is therefore incremental and never forces a breaking
signature change on a published extension.

## What this is not

- **Not a registry or service locator.** A `LifecycleGroup` holds only what you
  explicitly `Add`; there is no global, no `init()` registration, no discovery.
- **Not a DI container.** It manages *lifecycle*, not construction or wiring.
  Build and inject components as usual, then hand them to a group.
- **Not a health model.** `Ready` answers "can this serve right now"; rich
  component health checks remain `health.ComponentChecker` /
  `x/observability/ops/healthhttp`.

## Where it lives

`Lifecycle`, the phase interfaces, `Component`, and `LifecycleGroup` are in
`core` (the kernel owns process lifecycle). Extensions need not import `core` to
satisfy `Lifecycle` — Go's structural typing means a type with the right methods
already qualifies; only the wiring layer that builds a `LifecycleGroup` imports
`core`.
