# Plumego — Standard Library Web Toolkit

[![Go Reference](https://pkg.go.dev/badge/github.com/birdordev/plumego.svg)](https://pkg.go.dev/github.com/birdordev/plumego)
[![Go Version](https://img.shields.io/badge/Go-1.26%2B-00ADD8?style=flat&logo=go)](https://go.dev/)
[![CI](https://github.com/birdordev/plumego/actions/workflows/quality-gates.yml/badge.svg)](https://github.com/birdordev/plumego/actions/workflows/quality-gates.yml)
[![Go Report Card](https://goreportcard.com/badge/github.com/birdordev/plumego)](https://goreportcard.com/report/github.com/birdordev/plumego)
[![codecov](https://codecov.io/gh/birdordev/plumego/graph/badge.svg)](https://codecov.io/gh/birdordev/plumego)
[![Status](https://img.shields.io/badge/status-v1.1.0-blue)](https://github.com/birdordev/plumego/releases/tag/v1.1.0)
[![License](https://img.shields.io/badge/license-MIT-green)](LICENSE)

Plumego is a small Go HTTP toolkit built on the standard library, keeping
`net/http` compatibility at its center: handlers are ordinary
`func(http.ResponseWriter, *http.Request)`, middleware wraps `http.Handler`,
and application wiring stays explicit in your own `main` package.

The stable surface is intentionally narrow. Start with `core`, `router`,
`contract`, and `middleware`; add `security`, `store`, `health`, `log`, and
`metrics` only when those responsibilities are needed.

## Why Plumego

Use Plumego when a `net/http` service needs route groups, a common JSON
response and error format, and an explicit server lifecycle. The main Go
module has no third-party dependencies. Optional capabilities are wired
in at the application entry point.

## Quick Start

Requires Go 1.26 or later. Save this example as `main.go`.

```go
package main

import (
	"log"
	"net/http"

	"github.com/birdordev/plumego"
	"github.com/birdordev/plumego/contract"
)

func main() {
	app := plumego.New()
	if err := app.Get("/ping", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := contract.WriteResponse(w, r, http.StatusOK, map[string]string{"message": "pong"}, nil); err != nil {
			log.Printf("write ping response: %v", err)
		}
	})); err != nil {
		log.Fatal(err)
	}
	log.Fatal(http.ListenAndServe(":8080", app))
}
```

Run:

```bash
go mod init example.com/hello
go get github.com/birdordev/plumego@latest
go run main.go
```

Send `curl -fsS http://localhost:8080/ping` from another terminal. The JSON
response contains `"message":"pong"` under `data`.

In this example, `http.ListenAndServe` owns the listener, and `app` prepares
its handler on the first request. To apply `AppConfig` server settings such as
address, timeouts, or TLS, construct the app with `plumego.NewWithConfig`,
register routes, and call `app.Run()`. For explicit lifecycle control, use
`Prepare`, `Server`, and `Shutdown`. Use `core.New` to inject dependencies.
The full examples are in [Getting started](./docs/start/getting-started.md).

## Choose Your Starting Point

- **Quick start:** [Hello world](./examples/hello) and [error handling](./examples/error-handling).
- **Standard application:** [Standard service](./reference/standard-service) and its [CRUD tutorial](./reference/standard-service/TUTORIAL.md).
- **Feature integrations:** [Reference application catalog](./reference/README.md#feature-scenario-references).

| I want to build... | Start here | Package tier |
| --- | --- | --- |
| A plain JSON API | [Standard service](./reference/standard-service) → stable roots only | **GA** |
| Authenticated endpoints and request metrics | [Production service](./reference/production-service) → stable roots and `x/tenant` for tenant-aware profiles | GA + beta |
| REST resources with CRUD conventions | [`with-rest`](./reference/with-rest) → `x/rest` | beta |
| A multi-tenant SaaS API | [`with-tenant`](./reference/with-tenant) → `x/tenant` | beta |
| An API gateway or reverse proxy | [`with-gateway`](./reference/with-gateway) → `x/gateway` | beta |
| Real-time WebSocket features | [`with-websocket`](./reference/with-websocket) → `x/websocket` | beta |
| An AI-backed service | [`with-ai`](./reference/with-ai) → `x/ai/provider`, `session`, `streaming`, `tool` | beta surfaces |
| Application messaging | [`with-messaging`](./reference/with-messaging) → `x/messaging` | beta |
| Events / pubsub | [`with-events`](./reference/with-events) → `x/messaging/pubsub` | experimental |
| Frontend / static assets | [`with-frontend`](./reference/with-frontend) → `x/frontend` | beta |
| Protected ops / admin routes | [`with-ops`](./reference/with-ops) → `x/observability/ops` | experimental |
| A gRPC + HTTP service | [`with-rpc`](./reference/with-rpc) → `x/rpc` | experimental |
| Observability (Prometheus / OpenTelemetry) | [`with-observability`](./reference/with-observability) → `x/observability` | beta |
| A tenant administration console | [`with-tenant-admin`](./reference/with-tenant-admin) → `x/tenant` | beta |
| Webhook ingress and delivery | [`with-webhook`](./reference/with-webhook) → `x/messaging/webhook` | experimental |
| HTTP file upload and download | [File API primer](./docs/modules/x/fileapi/README.md) → `x/fileapi` | experimental |
| SQL writes, migrations, and outbox delivery | [SQL reference](./reference/with-sql) | standalone example |
| Browser forms, sessions, and OIDC | [Browser reference](./reference/with-browser) | standalone example |
| Server-sent events | [SSE reference](./reference/with-sse) | standalone example |

The reference apps follow the `reference/standard-service` layout and wire
extensions explicitly at the application entry point.

## stdlib comparison

| Feature | `http.ServeMux` | plumego |
| --- | --- | --- |
| Basic routing | `Handle` and `HandleFunc` accept method-qualified patterns such as `GET /users`. | `Get`/`Post`/`AddRoute` register a method, path, and handler. |
| Path parameters | Match `/users/{id}` and read it with `r.PathValue("id")`. | Match `/users/:id` and read it with `plumego.Param(r, "id")`. |
| Route groups | Register prefixed patterns or mount a sub-mux. | Groups apply a shared prefix. |
| Scoped middleware | Assemble handlers per subtree. | Register common middleware with `Group.Use` before adding its routes. |
| Named routes + reverse URL | Caller builds URLs manually. | Reverse URL generation via the app/router API. |
| Route freeze | Registrations can be added after serving starts. | `Prepare` freezes routes before serving. Direct `ServeHTTP` freezes routes when it first prepares the handler. |
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
| `store` | File and idempotency contracts, database helpers, an in-memory cache, and a small embedded KV store. |
| `health` | Health/readiness models for app and dependency status. |
| `log` | Minimal logging interfaces and a default logger. |
| `metrics` | Minimal metrics contracts (counters, gauges, timings, collectors). |

Optional capability families live under `x/*`.

## Performance and I/O

- Prepared apps reuse the compiled handler chain. Frozen routers use a direct
  lookup for static routes. See the [router primer](./docs/modules/router/README.md).
- Concurrent mutations in file-backed `store/kv` can share a snapshot replacement
  and sync. Each commit still writes the full dataset. See the [KV boundary](./docs/modules/store/README.md#kv-boundary).
- The gateway negotiates HTTP/2 with supported HTTPS upstreams and flushes
  server-sent events as upstream data arrives. See the [gateway primer](./docs/modules/x/gateway/README.md).

Run the router and application microbenchmarks on the current checkout.

```bash
go test ./router ./core -run '^$' -bench 'Benchmark(Opt|AppServeHTTP)' -benchmem -count=3 -cpu=1,4,8
```

The [v1.1.0 comparison results](./docs/evidence/benchmarks/results-v1.1.0.md)
measure the tagged release. Use workload-specific load tests to measure
end-to-end latency under concurrency.

## Current Status

The nine packages listed above are **stable roots**. Their public APIs and
documented behavior carry the `v1.x` compatibility guarantee.

Seven `x/*` extension families have **beta** entry points with compatibility
evidence across cited release refs. These are `x/frontend`, `x/gateway`,
`x/messaging`, `x/observability`, `x/rest`, `x/tenant`, and `x/websocket`.

Some experimental families contain selected beta surfaces, including parts of
`x/ai` and `x/data`. Some beta families also contain experimental subpackages.
Check the [extension maturity dashboard](./docs/concepts/extension-maturity.md)
for the status of the package you import. Experimental APIs may change in a
minor release.

See [stability](./docs/release/STABILITY.md) for the full v1 guarantee,
[compatibility](./docs/release/COMPATIBILITY.md) for upgrades, and the
[extension stability policy](./docs/reference/extension-stability-policy.md)
for promotion criteria.

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
