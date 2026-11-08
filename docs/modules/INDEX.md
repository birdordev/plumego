# docs/modules — Plumego Module Guide

This directory contains module primers for stable roots and extension packages. Use this page to find what you need, understand module boundaries, and plan your learning path.

## Stable Roots at a Glance

These 9 packages are the learning path for Plumego. They have zero external dependencies and carry a full v1 compatibility guarantee.

| Module | Purpose | When to use |
|--------|---------|------------|
| **`core`** | App construction, lifecycle, route registration | Starting every Plumego service |
| **`router`** | Route matching, path params, groups, metadata | Building complex route hierarchies |
| **`contract`** | Response writers, error builders, binding | Sending responses/errors in every handler |
| **`middleware`** | Middleware composition + standard packages | Composing cross-cutting concerns |
| **`security`** | Auth, JWT, passwords, abuse guards | Authentication, authorization, hardening |
| **`store`** | Storage contracts, in-memory primitives | Interacting with storage systems |
| **`health`** | Health/readiness models | Reporting service health to orchestrators |
| **`log`** | Logging interfaces + default logger | Structured logging in your handlers |
| **`metrics`** | Metrics contracts, collectors | Collecting application metrics |

**Next:** Pick a module from the list below and read its README.

## Module Boundaries

Understanding what each module owns helps you know where to look for what you need.

| Module | Owns | Does NOT own |
|--------|------|--------------|
| `core` | App, routes, middleware attachment, lifecycle | Response formatting, storage, business logic |
| `router` | Route matching, params, groups, metadata | Response building, request binding |
| `contract` | Success/error responses, request accessors | Validation, business DTO assembly |
| `middleware` | Transport-layer concerns (logging, recovery, CORS) | Business logic injection, policy branching |
| `security` | Auth adapters, JWT, password hashing | User/permission stores, business policy |
| `store` | Storage contracts + in-memory impls | ORM wiring, business repositories |
| `health` | Health models, status propagation | Business-logic health determination |
| `log` | Logging interfaces, default logger | Application business logging |
| `metrics` | Metrics contracts, collectors | What to measure (you decide) |

## Quick Lookup

**"I want to..."** → **Read this module**

- Route requests and handle params → `router/` README
- Send responses and errors → `contract/` README
- Add middleware (logging, auth, etc.) → `middleware/` README
- Handle panics gracefully → `middleware/recovery`
- Log requests → `middleware/accesslog`
- Hash passwords → `security/` README
- Get/set request context → `contract/` README
- Store data → `store/` README
- Report health status → `health/` README
- Export metrics → `metrics/` README

## Directory Structure

Directories use the following naming scheme:

| Directory pattern | Meaning | Example |
|---|---|---|
| `plumego/` | Root package convenience facade | `plumego/` |
| `<name>/` (no prefix) | Stable root package | `core/`, `contract/`, `middleware/` |
| `x/<family>/` | Top-level extension family | `x/ai/`, `x/gateway/`, `x/messaging/` |
| `x/<family>/<subpkg>/` | Sub-package of an extension family | `x/data/cache/`, `x/messaging/mq/` |

The directory tree under `docs/modules/x/` is a 1:1 mirror of the `x/` source tree.
The primer path is always `docs/modules/<import-path>/README.md`.

## Root Package

| Import path | Primer |
|---|---|
| `github.com/birdordev/plumego` | [`plumego/README.md`](plumego/README.md) |

## Extension Primers

| Import path | Primer |
|---|---|
| `x/ai` | [`x/ai/README.md`](x/ai/README.md) |
| `x/ai/distributed` | [`x/ai/distributed/README.md`](x/ai/distributed/README.md) |
| `x/ai/filter` | [`x/ai/filter/README.md`](x/ai/filter/README.md) |
| `x/ai/instrumentation` | [`x/ai/instrumentation/README.md`](x/ai/instrumentation/README.md) |
| `x/ai/llmcache` | [`x/ai/llmcache/README.md`](x/ai/llmcache/README.md) |
| `x/ai/marketplace` | [`x/ai/marketplace/README.md`](x/ai/marketplace/README.md) |
| `x/ai/metrics` | [`x/ai/metrics/README.md`](x/ai/metrics/README.md) |
| `x/ai/multimodal` | [`x/ai/multimodal/README.md`](x/ai/multimodal/README.md) |
| `x/ai/orchestration` | [`x/ai/orchestration/README.md`](x/ai/orchestration/README.md) |
| `x/ai/prompt` | [`x/ai/prompt/README.md`](x/ai/prompt/README.md) |
| `x/ai/provider` | [`x/ai/provider/README.md`](x/ai/provider/README.md) |
| `x/ai/resilience` | [`x/ai/resilience/README.md`](x/ai/resilience/README.md) |
| `x/ai/semanticcache` | [`x/ai/semanticcache/README.md`](x/ai/semanticcache/README.md) |
| `x/ai/semanticcache/cachemanager` | [`x/ai/semanticcache/cachemanager/README.md`](x/ai/semanticcache/cachemanager/README.md) |
| `x/ai/semanticcache/embedding` | [`x/ai/semanticcache/embedding/README.md`](x/ai/semanticcache/embedding/README.md) |
| `x/ai/semanticcache/vectorstore` | [`x/ai/semanticcache/vectorstore/README.md`](x/ai/semanticcache/vectorstore/README.md) |
| `x/ai/session` | [`x/ai/session/README.md`](x/ai/session/README.md) |
| `x/ai/sse` | [`x/ai/sse/README.md`](x/ai/sse/README.md) |
| `x/ai/streaming` | [`x/ai/streaming/README.md`](x/ai/streaming/README.md) |
| `x/ai/tokenizer` | [`x/ai/tokenizer/README.md`](x/ai/tokenizer/README.md) |
| `x/ai/tool` | [`x/ai/tool/README.md`](x/ai/tool/README.md) |
| `x/data` | [`x/data/README.md`](x/data/README.md) |
| `x/data/cache` | [`x/data/cache/README.md`](x/data/cache/README.md) |
| `x/data/file` | [`x/data/file/README.md`](x/data/file/README.md) |
| `x/data/idempotency` | [`x/data/idempotency/README.md`](x/data/idempotency/README.md) |
| `x/data/kvengine` | [`x/data/kvengine/README.md`](x/data/kvengine/README.md) |
| `x/data/migrate` | [`x/data/migrate/README.md`](x/data/migrate/README.md) |
| `x/data/pgx` | [`x/data/pgx/README.md`](x/data/pgx/README.md) |
| `x/data/rw` | [`x/data/rw/README.md`](x/data/rw/README.md) |
| `x/data/sharding` | [`x/data/sharding/README.md`](x/data/sharding/README.md) |
| `x/data/sqlx` | [`x/data/sqlx/README.md`](x/data/sqlx/README.md) |
| `x/fileapi` | [`x/fileapi/README.md`](x/fileapi/README.md) |
| `x/frontend` | [`x/frontend/README.md`](x/frontend/README.md) |
| `x/gateway` | [`x/gateway/README.md`](x/gateway/README.md) |
| `x/gateway/cache` | [`x/gateway/cache/README.md`](x/gateway/cache/README.md) |
| `x/gateway/discovery` | [`x/gateway/discovery/README.md`](x/gateway/discovery/README.md) |
| `x/gateway/ipc` | [`x/gateway/ipc/README.md`](x/gateway/ipc/README.md) |
| `x/gateway/protocol` | [`x/gateway/protocol/README.md`](x/gateway/protocol/README.md) |
| `x/gateway/protocolmw` | [`x/gateway/protocolmw/README.md`](x/gateway/protocolmw/README.md) |
| `x/gateway/transform` | [`x/gateway/transform/README.md`](x/gateway/transform/README.md) |
| `x/messaging` | [`x/messaging/README.md`](x/messaging/README.md) |
| `x/messaging/mq` | [`x/messaging/mq/README.md`](x/messaging/mq/README.md) |
| `x/messaging/pubsub` | [`x/messaging/pubsub/README.md`](x/messaging/pubsub/README.md) |
| `x/messaging/scheduler` | [`x/messaging/scheduler/README.md`](x/messaging/scheduler/README.md) |
| `x/messaging/webhook` | [`x/messaging/webhook/README.md`](x/messaging/webhook/README.md) |
| `x/observability` | [`x/observability/README.md`](x/observability/README.md) |
| `x/observability/dbinsights` | [`x/observability/dbinsights/README.md`](x/observability/dbinsights/README.md) |
| `x/observability/devtools` | [`x/observability/devtools/README.md`](x/observability/devtools/README.md) |
| `x/observability/featuremetrics` | [`x/observability/featuremetrics/README.md`](x/observability/featuremetrics/README.md) |
| `x/observability/ops` | [`x/observability/ops/README.md`](x/observability/ops/README.md) |
| `x/observability/recordbuffer` | [`x/observability/recordbuffer/README.md`](x/observability/recordbuffer/README.md) |
| `x/observability/testlog` | [`x/observability/testlog/README.md`](x/observability/testlog/README.md) |
| `x/observability/testmetrics` | [`x/observability/testmetrics/README.md`](x/observability/testmetrics/README.md) |
| `x/observability/tracer` | [`x/observability/tracer/README.md`](x/observability/tracer/README.md) |
| `x/observability/windowmetrics` | [`x/observability/windowmetrics/README.md`](x/observability/windowmetrics/README.md) |
| `x/openapi` | [`x/openapi/README.md`](x/openapi/README.md) |
| `x/resilience` | [`x/resilience/README.md`](x/resilience/README.md) |
| `x/rest` | [`x/rest/README.md`](x/rest/README.md) |
| `x/rpc` | [`x/rpc/README.md`](x/rpc/README.md) |
| `x/rpc/client` | [`x/rpc/client/README.md`](x/rpc/client/README.md) |
| `x/rpc/gateway` | [`x/rpc/gateway/README.md`](x/rpc/gateway/README.md) |
| `x/rpc/server` | [`x/rpc/server/README.md`](x/rpc/server/README.md) |
| `x/tenant` | [`x/tenant/README.md`](x/tenant/README.md) |
| `x/validate` | [`x/validate/README.md`](x/validate/README.md) |
| `x/websocket` | [`x/websocket/README.md`](x/websocket/README.md) |

## Maturity Status

Extension maturity (experimental / beta / ga) is tracked in
`docs/concepts/extension-maturity.md`. Module manifests (`<package>/module.yaml`) are
the machine-readable source of truth for status, owner, risk, and validation
commands.
