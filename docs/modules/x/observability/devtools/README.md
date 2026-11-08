# x/observability/devtools

> **Import path:** `github.com/birdordev/plumego/x/observability/devtools` — sub-package of [`x/observability`](../README.md). This primer mirrors the package path under `docs/modules/x/`.

## Purpose

`x/observability/devtools` provides debug-only routes, profiling endpoints, env reload helpers, and development metrics wiring.

## v1 Status

- `experimental` in the Plumego v1 support matrix
- Included in repository release scope, but compatibility is not frozen

## Use this module when

- the task is local debug surface behavior
- the task is pprof, config snapshot, metrics snapshot, or env reload support
- development-only diagnostics need explicit route registration

## Do not use this module for

- production admin policy
- application bootstrap
- default runtime behavior in `core`

## First files to read

- `x/observability/devtools/module.yaml`
- `x/observability/devtools/devtools.go`
- `README.md`

## Public entrypoints

- `New`
- `NewDevCollector`
- `pubsubdebug.New`

## Main risks when changing this module

- debug routes becoming enabled by default
- hidden runtime side effects
- production behavior accidentally depending on devtools

## Canonical change shape

- keep devtools opt-in
- keep debug handlers explicit and locally mounted
- keep debug runtime snapshot payloads in `x/observability/devtools`, not in `core`
- keep debug JSON success payloads as local typed DTOs instead of one-off maps
- do not move debug routes into `core`
- keep one canonical debug endpoint family under `/_debug/*`

## Canonical debug endpoints

- `/_debug/routes` for plain-text route listing
- `/_debug/routes.json` for machine-readable route discovery
- `/_debug/config` for runtime/config snapshot payloads
- `/_debug/info` for config plus build metadata
- `/_debug/middleware`, `/_debug/metrics`, and `/_debug/reload` for the
  remaining debug-only surfaces

## Boundary with bootstrap

- `x/observability/devtools` is a secondary capability root for local diagnostics, not a bootstrap surface
- keep devtools out of canonical application startup
- mount debug routes explicitly and gate them outside production defaults

## Production Relationship

Do not expose `x/observability/devtools` as a default production admin surface. Use it for
local development, test environments, or explicitly protected operator
environments where config snapshots, pprof, route listings, metrics snapshots,
or env reload helpers are acceptable.

`reference/production-service` intentionally does not mount `x/observability/devtools` or any
`/_debug/*` route. Its protected `/ops/metrics` example is the production
diagnostics pattern; debug routes remain an explicit opt-in for local or
separately protected environments.

For production operations:

- use stable middleware for request-scoped logging, metrics, tracing, and request IDs
- use `x/observability` for exporter and adapter wiring
- use `x/observability/ops` for protected admin and health HTTP orchestration
- leave `/_debug/*` routes unmounted unless the application owner has added an explicit access gate

## Validation commands

- `go test -race -timeout 60s ./x/observability/devtools/...`
- `go test -timeout 20s ./x/observability/devtools/...`
- `go vet ./x/observability/devtools/...`
