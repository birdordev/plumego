# reference/

This directory contains Plumego reference applications. Each app is a
self-contained Go module that demonstrates a specific capability or deployment
pattern. They are teaching tools, not production bundles — every middleware and
route decision is visible and intentional.

---

## Where to start

| If you want to… | Start here |
|---|---|
| Understand the canonical app layout | [`standard-service`](#standard-service) |
| Add production security and observability | [`production-service`](#production-service) |
| Add Prometheus metrics and distributed tracing | [`with-observability`](#with-observability) |
| Add an LLM backend | [`with-ai`](#with-ai) |
| Stream async events to HTTP clients | [`with-events`](#with-events) |
| Serve a static frontend | [`with-frontend`](#with-frontend) |
| Reverse-proxy to a backend service | [`with-gateway`](#with-gateway) |
| Add in-process pub/sub messaging | [`with-messaging`](#with-messaging) |
| Mount protected ops and metrics routes | [`with-ops`](#with-ops) |
| Expose a REST resource controller | [`with-rest`](#with-rest) |
| Host a gRPC service over HTTP | [`with-rpc`](#with-rpc) |
| Add multi-tenant resolution and policy | [`with-tenant`](#with-tenant) |
| Build a tenant administration plane | [`with-tenant-admin`](#with-tenant-admin) |
| Receive inbound webhooks (GitHub, Stripe) | [`with-webhook`](#with-webhook) |
| Add a WebSocket hub | [`with-websocket`](#with-websocket) |
| Build a transactional SQL API | [`with-sql`](./with-sql) |
| Serve HTML with browser sessions and OIDC | [`with-browser`](./with-browser) |
| Mount a streaming SSE endpoint | [`with-sse`](./with-sse) |
| Measure framework HTTP overhead | [`benchmark`](#benchmark) |

---

## Canonical references

### standard-service

The canonical Plumego application shape. Read this before any other reference
app. It teaches explicit middleware wiring, standard-library HTTP handlers, the
`run()`/`app.Start(ctx)` bootstrap pattern, and stable-root-only dependencies.

- No `x/*` imports.
- Every middleware is named and ordered explicitly in `internal/app/app.go`.
- Every route is named and registered explicitly in `internal/app/routes.go`.
- Has `AGENTS.md`, `CLAUDE.md`, `AGENT_TASKS.md`, `PRODUCTION_CHECKLIST.md`, `ARCHITECTURE.md`.

### production-service

Extends `standard-service` with production-grade security and observability:
bearer-token auth (fail-closed), abuse guard, tracing, HTTP metrics, access
logs, tenant context, and an explicit ops route. Shows the secure, hardened
baseline before adding `x/*` capabilities.

- All middleware from `standard-service` plus tracing, httpmetrics, and
  rate-limit abuse guard.
- Protected `GET /api/profile` (bearer + tenant) and `GET /ops/metrics` (bearer).
- Has `AGENT_TASKS.md`, `PRODUCTION_CHECKLIST.md`.

---

## Feature scenario references

Scenario apps demonstrate a focused capability or an integration across
related capabilities. The canonical application layout remains
`standard-service`; individual scenarios wire their optional dependencies
explicitly.

### with-observability

Adds `x/observability` Prometheus metrics and OpenTelemetry distributed tracing.
`httpmetrics.Middleware` is wired with a real `PrometheusCollector` instead of
the noop used in `standard-service`; `middleware/tracing.Middleware` is wired
with an `OpenTelemetryTracer`. Shows the path from noop to real observability
in a single focused diff.

- `GET /metrics` — Prometheus text exposition format, ready for scraping.
- `GET /api/v1/spans` — in-process spans for development (remove in production).
- `GET /api/v1/stats` — metrics summary in the standard JSON envelope.
- `MetricsHandler` demonstrates reading via the `metrics.StatsReader` interface.

### with-ai

Shows the stable-tier `x/ai` subpackages: `provider`, `session`, `streaming`,
and `tool`. Safe first path for adding an LLM backend with streaming responses,
conversation history, and function calling.

### with-events

Wires an HTTP app to asynchronous order events with in-process pub/sub
(`x/messaging/pubsub`), idempotent consumption, delayed retry, and optional
outbound webhook delivery.

### with-frontend

Serves a static frontend (embedded assets) alongside an API, using `x/frontend`
for asset embedding and cache-control.

### with-gateway

Adds `x/gateway` reverse proxy to route incoming requests to a backend service.
Shows proxy construction, backend configuration, and wildcard route mounting.

### with-messaging

Adds `x/messaging` in-process pub/sub broker: publish domain events from
handlers, subscribe from background workers, and fan out to multiple consumers.

### with-ops

Mounts protected `x/observability/ops` routes and stable request-observability
middleware. Shows the pattern for internal metrics and health aggregation without
exposing debug tooling in production.

### with-rest

Adds `x/rest` resource controllers to a service that still follows the
`standard-service` layout. Shows controller registration, resource routing, and
response standardization.

### with-rpc

Hosts a gRPC service with `x/rpc/server` and exposes it over HTTP through
`x/rpc/gateway`. One-shot demo: runs a single test request and exits; does not
use the long-running `run()` signal pattern.

### with-tenant

Adds `x/tenant` resolution, policy, quota, and rate limiting. Shows how to
extract tenant ID from headers, enforce per-tenant policies, and surface quota
errors.

### with-tenant-admin

Self-contained multi-tenant administrative plane. Demonstrates tenant lifecycle
operations, quota administration, usage recording, and fail-closed admin
authentication using `x/tenant` primitives.

### with-webhook

Adds `x/messaging/webhook` inbound webhook receivers for GitHub and Stripe.
Shows HMAC signature verification, event routing, and idempotent handler
dispatch.

### with-websocket

Adds `x/websocket` WebSocket server with hub, room broadcast, and client
lifecycle management. Shows hub construction, connection upgrade, and graceful
shutdown.

---

## Tooling

### benchmark

A separate Go module (`reference/benchmark`) for local HTTP throughput
benchmarks comparing Plumego against chi, gin, and echo. Keeps
comparison-only dependencies out of the main module.
