# plumego (root package)

## Purpose

`github.com/birdordev/plumego` is the convenience entry point for the toolkit.
It provides zero-config constructors and type aliases so new services only
need a single import to get started.

## v1 Status

- `ga` in the Plumego v1 support matrix
- Public compatibility is expected for the stable package surface

## Use this package when

- starting a new service and you want the minimal import path
- the default configuration (`:8080`, 30 s timeouts, HTTP/2 enabled) is
  sufficient for your use case

## Do not use this package for

- production services that need explicit logger injection — use `core.New`
- services that need TLS, custom timeouts, or HTTP/2 policy — use
  `core.New(core.DefaultConfig(), ...)` after overriding the fields you need
- packages within the plumego module — internal packages must import
  `core`, `router`, `contract`, etc. directly

## First files to read

- `plumego.go`
- `core/module.yaml`
- `core/app.go`

## Public entrypoints

- `New` — zero-config constructor; returns `*core.App` on `DefaultConfig`
- `NewWithConfig` — constructor with a caller-supplied `AppConfig`
- `AppConfig` — type alias for `core.AppConfig`
- `AppDependencies` — type alias for `core.AppDependencies`
- `DefaultConfig` — re-export of `core.DefaultConfig`
- `Param` — re-export of `router.Param`

## Construction pattern

```go
// Minimal (default config, no logger)
app := plumego.New()
app.Get("/ping", http.HandlerFunc(handler))
log.Fatal(http.ListenAndServe(":8080", app))

// Non-default address
cfg := plumego.DefaultConfig()
cfg.Addr = ":9090"
app := plumego.NewWithConfig(cfg)

// Full control (logger, TLS, lifecycle) — use core.New directly
app := core.New(core.DefaultConfig(), core.AppDependencies{Logger: logger})
```

## Boundary contract

This package is a thin facade. It:

- **Must only** re-export constructors and helpers from `core` and `router`
- **Must not** introduce new types (no custom context, handler, or response types)
- **Must not** be imported by any package within the plumego module
  (`forbidden_import_patterns` in `specs/dependency-rules.yaml` enforces this)
- **Must not** import `x/*` or any stable root other than `core` and `router`

## When to graduate to core.New

Switch from `plumego.New()` to `core.New(...)` when you need any of:

| Need | How |
|---|---|
| Structured logger | `core.New(cfg, core.AppDependencies{Logger: logger})` |
| Custom address / timeouts | Override fields on `core.DefaultConfig()` |
| TLS | Set `core.AppConfig.TLS.Enabled = true` |
| HTTP/2 policy | Set `core.AppConfig.HTTP2Enabled` |
| Explicit lifecycle (Prepare / Server / Shutdown) | Call `app.Prepare()` then `app.Server()` |
| Production middleware chain | See `reference/standard-service` |
