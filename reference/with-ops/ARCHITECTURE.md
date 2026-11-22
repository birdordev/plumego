# Architecture Notes — with-ops

This document explains the structural choices in this feature demo.

---

## Route ownership

The demo wires stable middleware through `core.App` and registers its own
`/` and `/metrics` endpoints. The ops handler takes the application as its
route registrar.

---

## Structure

```
main.go                — config, app assembly, and signal handling
internal/app/app.go    — middleware and HTTP serving
internal/app/routes.go — application routes and ops wiring
internal/handler/      — root and metrics handlers
```

---

## Key components

### `x/observability/ops` handler

```go
opsHandler := ops.New(ops.Options{
    Enabled: true,
    Auth:    ops.AuthConfig{Token: token},
    Hooks:   ops.Hooks{QueueStats: ...},
    Logger:  logger,
})
if err := opsHandler.RegisterRoutes(a.Core); err != nil { ... }
```

The ops handler registers its protected routes through `RegisterRoutes`.
See the production checklist for token and network configuration.

### Metrics collector

```go
collector := metrics.NewBaseMetricsCollector()
```

`metrics.BaseMetricsCollector` is a stable-root type. The `/metrics` endpoint
serves its in-process stats. `httpmetrics.Middleware(collector)` populates
it from every request.

### Middleware chain

```go
a.Core.Use(
    requestid.Middleware(),
    httpmetrics.Middleware(collector),
    accesslogMw,
    recoveryMw,
)
```

Request ID runs first so downstream middleware can read it. Metrics and
access logs wrap recovery to observe the final HTTP status.

---

## Shutdown

The signal context drives `core.App.RunContext`. The app closes the
listener and gives in-flight HTTP requests up to 15 seconds to drain.

---

## What this demo does not demonstrate

- TLS
- Custom ops hook implementations beyond `QueueStats`
