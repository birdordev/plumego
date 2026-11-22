# Docs Index

This directory is Plumego's human-readable documentation surface. It is now
organized by reader intent, so start from the section that matches the work in
front of you instead of scanning every file.

## Quick Path

1. [Getting started](./start/getting-started.md)
2. [Why Plumego](./start/why-plumego.md)
3. [Adoption path](./start/adoption-path.md)
4. [Standard service](../reference/standard-service/README.md)
5. [Canonical style guide](./reference/canonical-style-guide.md)

Use `specs/*` and `tasks/*` only when you need machine-readable routing,
quality gates, or execution surfaces.

## Directory Map

| Directory | Purpose |
| --- | --- |
| `start/` | Onboarding, fit, adoption path, and troubleshooting |
| `guides/` | Task guides, migration notes, and authoring workflows |
| `concepts/` | Architecture, boundaries, maturity, and design rationale |
| `modules/` | Stable-root and `x/*` module primers aligned with manifests |
| `reference/` | Canonical style, stability, scaffold, and deprecation policy |
| `operations/` | Agent/Codex workflow, context budgets, milestones, and gates |
| `release/` | Roadmap, release notes, release checklists, and promotion templates |
| `evidence/` | Benchmarks, extension evidence, and stable API snapshots |
| `assets/` | Reusable workflow and template assets |

## Scenario Entrypoints

Use `../reference/standard-service` as the canonical application shape, then
open the relevant capability primer:

| Scenario | Primary reads |
| --- | --- |
| REST API service | `start/getting-started.md`, `../reference/standard-service/README.md`, `../reference/with-rest/README.md`, `modules/x/rest/README.md` |
| Multi-tenant API | `../reference/with-tenant/README.md`, `modules/x/tenant/README.md`, `concepts/x-tenant-blueprint.md` |
| Edge gateway | `modules/x/gateway/README.md`, including `x/gateway/discovery` when discovery is explicitly selected |
| Realtime service | `modules/x/websocket/README.md`, `modules/x/messaging/README.md` |
| Messaging service | `../reference/with-messaging/README.md`, `modules/x/messaging/README.md`, including `x/messaging/mq` or `x/messaging/pubsub` only for narrow primitive work |
| Webhook ingress or delivery | `../reference/with-webhook/README.md`, `modules/x/messaging/README.md`, including `x/messaging/webhook` for narrow transport work |
| RPC transport | `../reference/with-rpc/README.md`, `modules/x/rpc/README.md`, starting with `x/rpc/server`, `x/rpc/client`, or `x/rpc/gateway` for the direct transport task |
| File API service | `../reference/standard-service/README.md`, `modules/x/fileapi/README.md`, `modules/x/data/README.md` for storage and metadata backends |
| AI service | `../reference/with-ai/README.md`, `modules/x/ai/README.md`, starting with provider, session, streaming, and tool subpackages |
| Observability and ops | `../reference/with-ops/README.md`, `modules/x/observability/README.md`, `modules/x/observability/ops/README.md`, `modules/x/observability/devtools/README.md` |
| Cross-family resilience primitives | `modules/x/resilience/README.md`, `concepts/x-ai-resilience-boundary.md` when AI wrappers are involved |

These paths identify first reads. They do not replace the canonical bootstrap
layout or promote experimental `x/*` APIs to stable status.

## Workflow Entrypoints

- `docs/operations/codex-workflow.md` — default agent working loop, prompt shapes, and stop conditions
- `docs/operations/agent-code-quality-rules.md` — agent preflight, review output, risk focus, and gate selection rules
- `docs/operations/agent-context-budget.md` — token-bounded context packages, task-card limits, and validation output compression
- `docs/operations/milestone-pipeline.md` — artifact contract for milestone, plan, card, verify, and PR handoffs

Related execution surfaces live outside `docs/`:

- `tasks/milestones/README.md` — milestone queue usage and lifecycle
- `tasks/cards/README.md` — task-card queue usage and ownership rules
- `specs/*` — machine-readable routing, taxonomy, dependency, quality, and validation rules

## Reference Apps

- `../reference/standard-service/README.md` — canonical application layout and the default first read
- `../reference/production-service/README.md` — production-oriented explicit wiring beyond the minimal canonical path
- `../reference/with-rest/README.md` — explicit `x/rest` mounting pattern
- `../reference/with-tenant/README.md` — explicit `x/tenant` mounting pattern
- `../reference/with-tenant-admin/README.md` — explicit tenant administration console mounting pattern
- `../reference/with-gateway/README.md` — explicit `x/gateway` reverse-proxy mounting pattern
- `../reference/with-websocket/README.md` — explicit `x/websocket` mounting pattern
- `../reference/with-ai/README.md` — wiring for the beta surfaces of `x/ai`
- `../reference/with-ops/README.md` — explicit observability and protected admin surface mounting pattern
- `../reference/with-observability/README.md` — explicit observability stack (tracing, metrics export) mounting pattern
- `../reference/with-messaging/README.md` — explicit app-facing messaging mounting pattern
- `../reference/with-events/README.md` — explicit event-driven pubsub architecture mounting pattern
- `../reference/with-webhook/README.md` — explicit webhook ingress and outbound delivery mounting pattern
- `../reference/with-frontend/README.md` — explicit embedded static-asset mounting pattern
- `../reference/with-rpc/README.md` — explicit `x/rpc` gRPC transport mounting pattern
- `../reference/benchmark/README.md` — benchmarking harness comparing Plumego against other routers

Use the [extension maturity dashboard](./concepts/extension-maturity.md) to
check maturity by import path. The [benchmark evidence](./evidence/benchmarks/README.md)
records release measurements and the commands to reproduce them.

## Authority Order

When guidance overlaps, use this order:

1. `AGENTS.md`
2. `docs/reference/canonical-style-guide.md`
3. `specs/*`
4. the touched module's `module.yaml`
5. existing local patterns in touched files
