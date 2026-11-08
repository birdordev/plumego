# Reference Applications

The `reference/` directory contains working Plumego applications. Each one
demonstrates a specific layout or capability with every middleware, route, and
dependency decision visible and explicit. They are teaching tools, not production
bundles.

This guide helps you find the right starting point and understand what each
reference is — and is not — intended to do.

## Start Here

**If you are new to Plumego, start with `reference/standard-service`.** It is the
canonical application layout: stable-root-only dependencies, no `x/*` imports, and
all middleware and routes declared explicitly. Every other reference app builds on
this shape by adding one capability at a time.

Minimal reading order for new projects:

1. [`docs/start/getting-started.md`](../start/getting-started.md) — smallest runnable example
2. [`reference/standard-service`](../../reference/standard-service) — canonical layout, recommended for production
3. One `with-*` reference from the table below when you need a specific capability

## Reference Selection Table

| Reference | Purpose | Extension | Stability | Copyable? |
|---|---|---|---|---|
| `standard-service` | Canonical app layout. Every new service starts here. | stable roots only | GA | Yes — via scaffold |
| `production-service` | Hardened baseline: bearer auth, abuse guard, tracing, ops metrics. | stable roots + `x/tenant/resolve` | GA | Manual copy |
| `with-rest` | CRUD resource controllers with `x/rest`. | `x/rest` | beta | No |
| `with-observability` | Prometheus metrics + OpenTelemetry distributed tracing. | `x/observability` | beta | No |
| `with-websocket` | WebSocket hub, echo demo, heartbeat, graceful shutdown. | `x/websocket` | beta | No |
| `with-gateway` | Reverse proxy and path rewrite to a configurable backend. | `x/gateway` | beta | No |
| `with-ops` | Protected admin and metrics routes. | `x/observability/ops` | beta | No |
| `with-frontend` | Static asset embedding and serving alongside an API. | `x/frontend` | beta | No |
| `with-tenant` | Per-tenant resolution, policy, quota, and rate limiting. | `x/tenant` | experimental | No |
| `with-tenant-admin` | Tenant lifecycle and quota administration. | `x/tenant` | experimental | No |
| `with-events` | Async events with in-process pub/sub and webhook delivery. | `x/messaging/pubsub` | experimental | No |
| `with-messaging` | In-process pub/sub broker and background workers. | `x/messaging` | experimental | No |
| `with-webhook` | Inbound webhook verification (GitHub, Stripe). | `x/messaging/webhook` | experimental | No |
| `with-ai` | LLM provider, sessions, streaming, and tool calling with a mock backend. | `x/ai` | experimental | No |
| `with-rpc` | gRPC server + HTTP/JSON transcoding gateway using in-process bufconn. | `x/rpc` | experimental | No |
| `benchmark` | HTTP throughput comparison vs. Chi, Gin, and Echo. | (comparison deps) | tooling | No |

Stability terms follow the same definitions used in `README.md`: **GA** means the
underlying packages carry a full `v1` compatibility guarantee; **beta** means
stable across cited release refs with minor caveats; **experimental** means APIs
may change in any minor version without notice. See
[`docs/reference/extension-stability-policy.md`](./extension-stability-policy.md)
for the full policy.

## Recommended Learning Path

### Minimal path (covers most services)

1. **`reference/standard-service`** — understand the canonical layout: middleware
   order, route registration, the `contract` response envelope, config loading, and
   graceful shutdown. Read its `ARCHITECTURE.md` for the rationale behind each
   decision.
2. **`reference/production-service`** — when you are ready to harden: bearer-token
   auth with fail-closed behavior, per-IP abuse guard, distributed tracing, HTTP
   metrics, and a protected ops route.
3. **One `with-*` reference** for any additional capability your service needs,
   chosen from the table above.

### Capability lookup

Once you understand `standard-service`, add exactly one capability at a time:

| I need… | Read this reference |
|---|---|
| A plain JSON API with explicit routing | `standard-service` (you are already here) |
| Production security and observability hardening | `production-service` |
| CRUD resources with standardized route conventions | `with-rest` |
| Prometheus metrics + distributed tracing | `with-observability` |
| Real-time WebSocket connections | `with-websocket` |
| Reverse proxy to a backend service | `with-gateway` |
| Protected admin health aggregation and metrics routes | `with-ops` |
| Embedded static frontend assets served alongside an API | `with-frontend` |
| Per-tenant isolation, policy, and quota enforcement | `with-tenant` |
| Tenant lifecycle and quota administration | `with-tenant-admin` |
| Async event processing and pub/sub | `with-events` |
| In-process message broker and background workers | `with-messaging` |
| Inbound webhooks with HMAC signature verification | `with-webhook` |
| LLM provider integration with sessions and tool calling | `with-ai` |
| gRPC service hosted alongside an HTTP JSON API | `with-rpc` |
| HTTP throughput numbers to compare against other routers | `benchmark` |

## Maturity and Copyability

### Designed to copy: `standard-service`

`reference/standard-service` is the only reference designed as a copyable starting
point. The CLI scaffold generates a project with the same runtime shape:

```bash
plumego new myapp --template canonical
```

To copy it manually from the repository:

1. Rename the module in `go.mod` to your own module path (e.g. `github.com/yourorg/myapp`).
2. Remove the `replace` directive and pin the real published version of `github.com/birdordev/plumego`.
3. Replace the `standard-service/` import prefix in all `.go` files with your new module path.

Read `reference/standard-service/PRODUCTION_CHECKLIST.md` before deploying. In
particular: set `APP_WRITE_KEY` and `APP_CORS_ALLOWED_ORIGINS`, add rate limiting
on public endpoints, and swap the noop metrics collector for a real one.

### Hardened baseline for manual adoption: `production-service`

`reference/production-service` extends `standard-service` with bearer-token
authentication (fail-closed on missing or invalid tokens), per-IP abuse guard,
tracing middleware, HTTP metrics, tenant context injection, and a protected
`/ops/metrics` route. It is a suitable starting point for services that need these
capabilities from day one.

Copy it the same way as `standard-service` — rename the module, drop the `replace`
directive, and replace the import prefix — then read its `PRODUCTION_CHECKLIST.md`
before deploying.

### Wiring guides, not project templates: `with-*`

All `with-*` references are **integration demos, not starters**. They show the
minimal diff needed to wire one capability into a `standard-service`-shaped app.
Treat them as wiring examples you read alongside the module docs for the relevant
`x/*` package:

- Storage is always in-memory; there is no database, cache, or external service.
- Authentication, pagination, and unrelated capabilities are intentionally absent.
- They do not carry the production checklist that `standard-service` provides.

If you need two capabilities, combine their wiring yourself using `standard-service`
as the base and the relevant `with-*` apps as wiring references.

### Tooling: `benchmark`

`reference/benchmark` lives in its own Go module to keep comparison dependencies
(Chi, Gin, Echo) out of the main module. It is not a service template. See
`docs/evidence/benchmarks/README.md` for captured results from prior releases.

## What References Intentionally Avoid

All reference applications share these constraints:

**No external runtime dependencies.** Storage is always in-memory so that each
reference runs with `go run .` on a clean checkout. Replace in-memory repositories
with real ones in your own service; the interface stays the same.

**No automatic route discovery.** Every route is registered explicitly in
`internal/app/routes.go`. Reflection-based routing and controller scanning are
absent by design.

**No hidden globals or `init()` registration.** Middleware, routes, and
dependencies are wired at construction time in one visible place. Nothing is
registered by import side-effect.

**One capability per `with-*` reference.** Each `with-*` app adds exactly one
`x/*` extension family to the `standard-service` shape. This keeps the wiring
minimal and the diff readable.

**No complete business logic.** Domain objects are stubs or demos. References
teach wiring patterns, not domain modeling.

References do not replace the production configuration work your own service
requires. The `PRODUCTION_CHECKLIST.md` in each canonical reference covers the
items that must be addressed before deployment.

## How to Validate a Reference

Each reference app is a self-contained Go module. Run its tests from its own
directory:

```bash
cd reference/<name>
go test -race -timeout 30s ./...
```

To check the whole repository after editing reference-adjacent files:

```bash
make validate-diff   # minimal gate: vet, boundary checks, format
make gates           # full gate: race tests, coverage ≥90%, generated file freshness
```

The boundary checks that references are subject to:

```bash
go run ./internal/checks/dependency-rules   # stable roots must not import x/*
go run ./internal/checks/reference-layout   # directory shape matches the canonical pattern
go run ./internal/checks/module-manifests   # module metadata is present and valid
```

## Related Docs

- [`docs/start/getting-started.md`](../start/getting-started.md) — smallest runnable example and bootstrap progression
- [`docs/start/adoption-path.md`](../start/adoption-path.md) — 5-minute, 30-minute, 1-day sequence
- [`reference/standard-service/ARCHITECTURE.md`](../../reference/standard-service/ARCHITECTURE.md) — layout rationale and dependency direction
- [`docs/reference/canonical-style-guide.md`](./canonical-style-guide.md) — handler, middleware, routing, and DI conventions
- [`docs/modules/`](../modules/) — package-specific primers for each `x/*` extension
- [`reference/README.md`](../../reference/README.md) — quick lookup table for all reference apps
- [`docs/reference/extension-stability-policy.md`](./extension-stability-policy.md) — full compatibility policy and promotion criteria
- [`docs/evidence/benchmarks/README.md`](../evidence/benchmarks/README.md) — benchmark results vs. Chi, Gin, Echo
