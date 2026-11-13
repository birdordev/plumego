# core

## Purpose

`core` is the HTTP application kernel. It owns app construction, route attachment, middleware attachment, and server lifecycle.

## v1 Status

- `ga` in the Plumego v1 support matrix
- Public compatibility is expected for the stable package surface

## Use this module when

- assembling an application from explicit routes and middleware
- starting or stopping an HTTP server
- wiring passive runtime dependencies such as logging, and attaching metrics
  through middleware or app-local wiring

## Do not use this module for

- route matching
- feature catalogs or plugin containers
- tenant policy
- persistence behavior

## First files to read

- `core/module.yaml`
- `core/app.go`
- `core/config.go`
- `core/routing.go`
- `core/middleware.go`
- `core/lifecycle.go`
- `core/dependencies.go`
- `reference/standard-service/internal/app/app.go`

## Public entrypoints

- `New`
- `App`
- `AppDependencies`
- `DefaultConfig`
- `AppConfig`
- `TLSConfig`
- `RouterConfig`
- `PreparationState`
- `(*App).Use`
- `(*App).AddRoute`
- `(*App).Get`
- `(*App).Post`
- `(*App).Put`
- `(*App).Delete`
- `(*App).Patch`
- `(*App).Any`
- `(*App).PreparationState`
- `(*App).URL`
- `(*App).Routes`
- `(*App).Logger`
- `(*App).Prepare`
- `(*App).Server`
- `(*App).Shutdown`

## Configuration contract

`DefaultConfig()` is the supported baseline for application code. Custom
`AppConfig` values are copied by value during `New`, so later mutations to the
caller-owned config value do not affect the app.

Server hardening fields follow standard `http.Server` zero-value semantics:
zero timeouts disable that specific timeout and `MaxHeaderBytes == 0` leaves the
server on the standard library default. `Prepare` rejects empty addresses and
negative timeout/header-size values before freezing route or middleware
mutation. `DrainInterval <= 0` uses core's default drain logging interval.

`HTTP2Enabled` controls the prepared server's TLS HTTP/2 policy. When false,
core installs an empty `TLSNextProto` map on the returned `*http.Server`, which
matches the standard-library mechanism for disabling automatic TLS HTTP/2
configuration. It is not an h2c or universal protocol-negotiation switch.

TLS ownership is intentionally narrow. The stable core TLS API is basic
certificate/key loading through `TLSConfig.Enabled`, `TLSConfig.CertFile`, and
`TLSConfig.KeyFile`. `Prepare` loads that material into the prepared
`*http.Server`; callers own advanced TLS policy such as minimum versions, cipher
suites, client certificates, and custom `GetCertificate` behavior by adjusting
`Server().TLSConfig` before serving. Core intentionally does not expose
additional TLS policy hooks in `AppConfig`.

`Server()` returns the prepared `*http.Server` to preserve standard-library
compatibility. Direct caller overrides are caller-owned: replacing `Handler`
bypasses the prepared core handler and middleware chain, replacing `ConnState`
bypasses core open-connection tracking, replacing `TLSConfig` replaces the
loaded TLS material, and replacing `TLSNextProto` changes the prepared HTTP/2
policy.

Logger ownership is passive. `core.New` resolves a missing
`AppDependencies.Logger` to a discard logger, and `(*App).Logger()` also returns
a discard logger when the app logger field is missing. App methods require a
non-nil `*App`; calling methods on a nil receiver is treated as a programmer
error rather than a recoverable lifecycle state. Core does not keep a package-level
fallback logger singleton and does not start, stop, flush, or close logger
implementations for the caller.

## Error contract

Core lifecycle and wiring entrypoints return normal Go errors. Errors include
stable `core <operation>` diagnostic context, may include operation parameters
before the final cause, include stable operation names such as `prepare_server`,
`get_server`, `shutdown_app`, `add_route`, and `use_middleware`, and wrap
lower-level causes where a caller-owned dependency or the standard library
produced the failure.

The stable core surface intentionally does not export lifecycle sentinel errors.
Callers should handle errors at the operation boundary, use `errors.Is` only for
wrapped lower-level causes that are already exported by the producing package,
and avoid depending on ad hoc string parsing for branching behavior. Operation
names are stable diagnostic context, not a typed lifecycle error taxonomy.

## Main risks when changing this module

- startup regression
- shutdown regression
- route assembly regression

## Canonical change shape

- keep bootstrap explicit
- construct apps through `DefaultConfig()` / `AppConfig`, and use `AppDependencies` as the explicit constructor dependency contract
- treat `DefaultConfig()` as the supported baseline: `Prepare` rejects empty server addresses and negative server hardening values before freezing route/middleware mutation; zero timeout fields keep standard `http.Server` semantics
- keep lifecycle behavior reviewable
- keep one canonical lifecycle path: `Prepare` + `Server` + `Shutdown`
- split handler preparation from server preparation: `ServeHTTP` only freezes config/router state and builds the handler, while `Prepare` is the explicit path that validates server-only config, allocates `http.Server`, and prepares connection tracking
- keep server-only preparation validation non-destructive: TLS config and certificate load failures return before route/middleware mutation is frozen
- keep TLS on the same public serve path: `Prepare` loads configured certificates into the returned `*http.Server`, and callers use `ListenAndServeTLS("", "")` on that prepared server when TLS is enabled
- keep `core` as the first-party router owner: route wiring goes through
  `App.AddRoute` or the registered method helpers; `App.AddRoute` is the
  canonical path when route options such as `router.WithRouteName(...)` are
  needed, and reverse URL lookup goes through `App.URL(...)`, not raw router
  replacement or mutation
- do not add more route convenience wrappers; new protocol or feature-specific
  route factories belong in application wiring or the owning extension
- keep HTTP request metrics explicit in middleware/app-local wiring; `core` does not own live observer attachment state
- keep readiness ownership out of `core`; callers own the outer serve loop, so readiness signaling must stay app-local instead of pretending the kernel knows when traffic can flow
- keep logger subsystem ownership out of `core`; `core.AppDependencies{Logger: ...}` injects a passive dependency and callers own logger initialization and flushing
- keep app-local debug flags and env-file metadata outside `core`; the kernel owns HTTP runtime state, not devtools metadata transport
- keep debug/runtime snapshot payloads in `x/observability/devtools`; `core` owns lifecycle state, not tooling response contracts
- keep the app logger kernel-owned on `App.Logger()`; `router` stays logger-free and does not mirror app logger state
- keep router behavior policy in typed config, not in ad hoc constructor options
- push feature-specific wiring back to app-local code or the owning extension
- preserve `net/http` compatibility while keeping `core` as a kernel

## Frozen behavior matrix

These behaviors are part of the current stable-root freeze baseline:

| Surface | Behavior |
| --- | --- |
| Construction | `New` copies `AppConfig` by value and resolves missing dependencies to safe defaults |
| Preparation state | `PreparationState` names the stable mutation, handler-prepared, and server-prepared phases exposed by core; `(*App).PreparationState()` returns the current phase as a read-only snapshot and returns the empty value for zero-value apps |
| Config validation | `Prepare` rejects invalid server config before freezing route/middleware mutation |
| Route wiring | `AddRoute` and method helpers delegate to the owned router with explicit method/path handlers |
| Middleware wiring | `Use` preserves registration order, treats an empty middleware list as a no-op, and rejects nil middleware without partial registration |
| `ServeHTTP` | lazily prepares the handler only, skips server-only config validation, freezes later route/middleware mutation, and remains `net/http` compatible |
| `ServeHTTP` then failed `Prepare` | if direct handler use already put the app in `handler_prepared`, a later server-only config failure from `Prepare` keeps the app handler-prepared, leaves `Server()` unavailable, and does not reopen route or middleware mutation |
| `Prepare` | freezes handler state, builds one `http.Server`, prepares open HTTP connection tracking, applies the configured TLS HTTP/2 policy, and is idempotent |
| `Prepare` failure | while the app is still mutable, server-only config errors return before freezing route/middleware mutation |
| `Server` | returns an error before explicit server preparation and returns the prepared server after `Prepare` |
| `Shutdown` | requires a prepared server, tolerates nil contexts by using `context.Background()`, delegates shutdown to `http.Server`, starts open-connection drain logging for one shutdown attempt at a time, and releases it when the last connection closes or the owning shutdown returns. A later shutdown can retry while connections remain open |
| Post-shutdown | after successful shutdown, the app remains `server_prepared`, `Server()` and `Prepare()` keep returning the same closed `*http.Server`, repeated `Shutdown` is accepted, and `ServeHTTP` can still serve through the prepared handler; caller code must create a new app to prepare a fresh server |
| TLS | `Prepare` loads configured cert/key material into the returned `*http.Server` |
| Ownership | logger lifecycle, readiness signaling, debug routes, and feature wiring remain caller-owned |
| `LifecycleGroup` | The first `Start` that enters startup freezes registration. A context already canceled leaves the group idle. Startup failure or cancellation during startup rolls back every attempted component in reverse order with the startup context and joins startup and cleanup errors. `Stop` can retry pending cleanup and treats a nil context as `context.Background()`. `Ready` probes components only after successful startup, and overlapping operations return without waiting for callbacks. |

Focused regression coverage lives in `core/freeze_test.go`,
`core/lifecycle_test.go`, `core/app_test.go`, and `core/routing_test.go`.
