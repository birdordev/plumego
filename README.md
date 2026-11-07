# Plumego — Standard Library Web Toolkit

[![Go Reference](https://pkg.go.dev/badge/github.com/spcent/plumego.svg)](https://pkg.go.dev/github.com/spcent/plumego)
[![Go Version](https://img.shields.io/badge/Go-1.26%2B-00ADD8?style=flat&logo=go)](https://go.dev/)
[![CI](https://github.com/spcent/plumego/actions/workflows/quality-gates.yml/badge.svg)](https://github.com/spcent/plumego/actions/workflows/quality-gates.yml)
[![Go Report Card](https://goreportcard.com/badge/github.com/spcent/plumego)](https://goreportcard.com/report/github.com/spcent/plumego)
[![codecov](https://codecov.io/gh/spcent/plumego/graph/badge.svg)](https://codecov.io/gh/spcent/plumego)
[![Status](https://img.shields.io/badge/status-v1.1.0-blue)](https://github.com/spcent/plumego/releases/tag/v1.1.0)
[![License](https://img.shields.io/badge/license-MIT-green)](LICENSE)

Plumego is a small Go HTTP toolkit built on the standard library, keeping
`net/http` compatibility at its center: handlers are ordinary
`func(http.ResponseWriter, *http.Request)`, middleware wraps `http.Handler`,
and application wiring stays explicit in your own `main` package.

The stable surface is intentionally narrow. Start with `core`, `router`,
`contract`, and `middleware`; add `security`, `store`, `health`, `log`, and
`metrics` only when those responsibilities are needed.

## Why Plumego

For Go services that need more structure than raw `http.ServeMux` without taking on a large framework model.

**Choose Plumego if you:**
- Want to understand every line of your HTTP server's wiring
- Prefer stdlib shapes and patterns
- Expect your service to live for years with predictable maintenance
- Use code agents (Claude, Codex, Cursor) to assist development
- Value small, testable, refactorable code over convenience

**Plumego is NOT:**
- A "Gin/Echo replacement" (we're complementary to stdlib, not competitive with frameworks)
- The fastest option (we optimize for clarity, not throughput)
- "Batteries-included" (optional `x/*` extensions don't bloat the core)
- For teams who want zero wiring code

| Toolkit | Position | Best for |
|---------|----------|----------|
| `http.ServeMux` | Minimal routing | Learning, trivial services |
| **Plumego** | **Thin layer on stdlib** | **Production services with stable maintenance** |
| Chi | Lightweight router | Function-builder middleware style |
| Gin | Fast + convenient | High-velocity prototyping |
| Echo | Feature-rich | Fully-featured applications |
| Fiber | High performance | Maximum throughput |

Read [`docs/start/POSITIONING.md`](./docs/start/POSITIONING.md) for a deeper explanation of the design philosophy and when to choose Plumego.

## Quick Start

`main.go`:

```go
package main

import (
	"log"
	"net/http"

	"github.com/spcent/plumego"
	"github.com/spcent/plumego/contract"
)

func main() {
	app := plumego.New()
	app.Get("/ping", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = contract.WriteResponse(w, r, http.StatusOK, map[string]string{"message": "pong"}, nil)
	}))
	log.Fatal(http.ListenAndServe(":8080", app))
}
```

Run:

```bash
go mod init example.com/hello
go get github.com/spcent/plumego@latest
go run main.go
```

Open `http://localhost:8080/ping`. For a production-style layout, read
[`reference/standard-service`](./reference/standard-service) next.

For non-default address, timeouts, or TLS:

```go
cfg := plumego.DefaultConfig()
cfg.Addr = ":9090"
app := plumego.NewWithConfig(cfg)
```

For production wiring with explicit logger injection, use `core.New` directly —
see [`docs/start/getting-started.md`](./docs/start/getting-started.md).

## Choose Your Starting Point

| I want to build... | Start here | Tier |
| --- | --- | --- |
| A plain JSON API | `reference/standard-service` → stable roots only | **GA** |
| REST resources with CRUD conventions | `reference/with-rest` → `x/rest` | beta |
| A multi-tenant SaaS API | `reference/with-tenant` → `x/tenant` | beta |
| An API gateway or reverse proxy | `reference/with-gateway` → `x/gateway` | beta |
| Real-time WebSocket features | `reference/with-websocket` → `x/websocket` | beta |
| An AI-backed service | `reference/with-ai` → `x/ai/provider` | experimental |
| A service with rich messaging/webhooks | `reference/with-messaging` → `x/messaging` | beta |
| Events / pubsub | `reference/with-events` → `x/messaging` | beta |
| Frontend / static assets | `reference/with-frontend` → `x/frontend` | beta |
| Protected ops / admin routes | `reference/with-ops` → stable roots + `security` | GA |
| A gRPC + HTTP service | `reference/with-rpc` → `x/rpc` | experimental |
| Observability (Prometheus / OpenTelemetry) | `reference/with-observability` → `x/observability` | beta |
| A tenant administration console | `reference/with-tenant-admin` → `x/tenant` | beta |
| Webhook ingress / delivery | `reference/with-webhook` → `x/messaging` | beta |

All paths keep `reference/standard-service` as the base layout; extensions are
explicit additions, not alternate bootstraps.

## stdlib comparison

| Feature | `http.ServeMux` | plumego |
| --- | --- | --- |
| Basic routing | Method handling is caller-owned. | `Get`/`Post`/`AddRoute` register one method, path, handler. |
| `{param}` extraction | Caller parses path segments manually. | Router matches params; read from request context. |
| Route groups | Caller repeats prefixes manually. | Groups apply a shared prefix. |
| Per-group middleware | Caller composes handlers per subtree. | Groups carry shared middleware, keeping `http.Handler` shape. |
| Named routes + reverse URL | Caller builds URLs manually. | Reverse URL generation via the app/router API. |
| Route freeze | Routes mutable whenever wiring changes. | `Prepare` freezes routes before serving. |
| Structured errors | Caller defines every response shape. | `contract.WriteError` is the canonical error path. |
| Request ID carriage | Caller picks and propagates a convention. | Explicit context accessors + middleware support. |
| Graceful lifecycle | Caller builds setup and shutdown policy. | `Prepare`, `Server`, `Shutdown` keep it explicit and reusable. |

## Package overview

| Package | Role |
| --- | --- |
| `core` | App construction, route registration, middleware attachment, server lifecycle. |
| `router` | Route matching, path params, groups, metadata, reverse URL generation. |
| `contract` | Response writers, structured error builders, request metadata, transport binding. |
| `middleware` | Transport-only middleware composition and first-party packages. |
| `security` | Auth, JWT, password, security headers, input-safety, abuse guards. |
| `store` | Stable storage contracts and in-memory primitives (cache, KV, file, DB, idempotency). |
| `health` | Health/readiness models for app and dependency status. |
| `log` | Minimal logging interfaces and a default logger. |
| `metrics` | Minimal metrics contracts (counters, gauges, timings, collectors). |

Optional capability families live under `x/*` — additions to the stable root
path, not alternate layouts.

## Current Status

The nine packages listed above are **stable roots** carrying a full `v1`
compatibility guarantee: signatures, package names, and behaviour are frozen for
the `v1.x` release series.

Seven `x/*` extension families are **beta** — stable across cited release refs
and suitable for production adoption with minor caveats: `x/frontend`,
`x/gateway`, `x/messaging`, `x/observability`, `x/rest`, `x/tenant`, and
`x/websocket`.

All remaining `x/*` extensions are **experimental**: APIs may change in any
minor version without notice. Do not use them in production services without
explicit project-level stabilization.

See [`STABILITY.md`](./docs/release/STABILITY.md) for the full v1 guarantee, [`docs/release/COMPATIBILITY.md`](./COMPATIBILITY.md) for upgrade paths, and [`docs/reference/extension-stability-policy.md`](./docs/reference/extension-stability-policy.md) for detailed promotion criteria.

## Agent-First Design

Plumego is maintained with an agent-first control plane: `docs/` explains the
architecture, `specs/` records machine-checkable boundaries, `tasks/` defines
reviewable execution units, and `reference/` shows canonical wiring. Checks
under `internal/checks/` enforce key boundaries locally and in CI, so automated
changes produce reviewable evidence instead of implicit convention. See
[`docs/concepts/agent-first.md`](./docs/concepts/agent-first.md) for the model
and adoption path, and
[`docs/operations/agent-first-operating-reference.md`](./docs/operations/agent-first-operating-reference.md)
for the internal operating reference.

## Getting Help

**First time here?**
- [`docs/start/POSITIONING.md`](./docs/start/POSITIONING.md) — Why Plumego exists and when to use it
- [`docs/start/getting-started.md`](./docs/start/getting-started.md) — smallest runnable walkthrough
- [`docs/start/adoption-path.md`](./docs/start/adoption-path.md) — 5-minute, 30-minute, 1-day learning flow

**Building an app?**
- [`reference/standard-service`](./reference/standard-service) — canonical app layout
- [`docs/reference/canonical-style-guide.md`](./docs/reference/canonical-style-guide.md) — handler, middleware, routing, DI conventions
- [`docs/reference/reference-apps.md`](./docs/reference/reference-apps.md) — guide to choosing a reference application

**Choosing technology?**
- [`STABILITY.md`](./docs/release/STABILITY.md) — what's stable, beta, and experimental in v1
- [`COMPATIBILITY.md`](./docs/release/COMPATIBILITY.md) — upgrade paths and migration guides
- [`docs/modules`](./docs/modules) — package-specific primers

**Troubleshooting?**
- [`docs/start/troubleshooting.md`](./docs/start/troubleshooting.md) — route freeze, middleware order, JWT errors, lifecycle issues
- [`docs/evidence/benchmarks/README.md`](./docs/evidence/benchmarks/README.md) — performance vs Chi, Gin, Echo

**Contributing?**
- [`CONTRIBUTING.md`](./CONTRIBUTING.md) — development setup, validation gates, and the PR process
- [`SECURITY.md`](./SECURITY.md) — supported versions and private vulnerability reporting
- [`CHANGELOG.md`](./CHANGELOG.md) — release history
