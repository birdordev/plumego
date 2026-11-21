# x/observability/ops

> **Import path:** `github.com/birdordev/plumego/x/observability/ops` — sub-package of [`x/observability`](../README.md). This primer mirrors the package path under `docs/modules/x/`.

## Purpose

`x/observability/ops` provides protected operations endpoints for queues, receipts, health HTTP orchestration, and runtime diagnostics.

## v1 Status

- `experimental` in the Plumego v1 support matrix
- Included in repository release scope, but compatibility is not frozen

## Use this module when

- the task is an auth-gated admin endpoint
- the task is queue or receipt diagnostics
- the task is health HTTP manager orchestration, history, metrics, or protected health diagnostics
- the task is a protected runtime operations surface

## Do not use this module for

- generic observability adapter work
- application bootstrap
- unauthenticated public diagnostics

## First files to read

- `x/observability/ops/module.yaml`
- `specs/task-routing.yaml`
- `x/observability/ops/ops.go`
- `x/observability/ops/healthhttp`

## Main risks when changing this module

- weakened auth boundaries
- hidden admin side effects
- mixing broader observability concerns into protected ops endpoints

## Canonical change shape

- keep admin routing explicit and auth-gated
- keep health manager execution policy in `x/observability/ops/healthhttp`, not stable `health`
- Health aggregation publishes consistent health and readiness results, including empty and canceled checks. Unknown component states fail closed. Replacing a registered component invalidates in-flight results for its previous registration.
- Concurrent checks of one component publish only the latest-started check result; an older completion cannot overwrite a newer failure.
- Checker failures use a fixed message in health models and HTTP diagnostics, while `CheckComponent` still returns the original checker error for programmatic classification. Callers must handle that error without logging credential-bearing text.
- Readiness snapshots detach component maps. Closing a health manager makes it unhealthy and not ready; in-flight checks and later `MarkReady` calls cannot restore readiness. Checkers must cooperate with their context to bound execution time.
- use `x/observability/ops` only for protected admin surfaces
- keep broader observability adapter work in `x/observability`
- `x/observability/ops/healthhttp` uses the standard JSON envelope. Health documents preserve their status code through a shared writer with an encoding fallback. Liveness text and health history CSV remain raw responses.
- `x/observability/ops/healthhttp` diagnostics JSON success payloads use typed DTO structs instead of one-off maps
- Health status responses are encoded before sending headers. An encoding
  failure returns a fixed error message and preserves an unhealthy 503.
  Readiness responses omit component detail data.
- protected ops JSON errors use `contract.WriteError` with explicit uppercase codes; not-configured hooks use `*_NOT_CONFIGURED`, hook failures use `*_FAILED`, and health-history query failures use `INVALID_QUERY` with safe messages
- hook failure logging records safe metadata only; shared admin transport code must not log raw hook error text because hook errors can contain downstream identifiers or credentials

## Boundary with x/observability

- `x/observability/ops`: protected admin endpoints, health HTTP manager orchestration, auth-gated diagnostics, and runtime control surfaces
- `x/observability`: broader adapter, export, and diagnostics integration work
- stable `middleware/*`: transport-only observability primitives

## Production Profile

Use `x/observability/ops` only after an application has an explicit admin auth boundary.
Production routes should be mounted in app-local wiring, protected by
`middleware/auth` or an equivalent caller-owned gate, and kept separate from
public liveness/readiness endpoints.

`reference/production-service` follows the same production shape for its
app-local ops route: `/ops/metrics` is mounted explicitly and protected by a
Bearer token from `OPS_TOKEN`. If the token is unset, the route fails closed.
`/healthz` and `/readyz` stay separate probe endpoints and do not imply access
to admin diagnostics. The reference `/api/status` route reports the configured
ops policy without exposing token values.

Recommended ownership:

- public liveness/readiness: app-local handlers or `x/observability/ops/healthhttp` when the
  service needs protected health orchestration and history
- protected runtime diagnostics: `x/observability/ops`
- telemetry export and adapter wiring: `x/observability`
- local debug and reload tools: `x/observability/devtools`, not production defaults


Aggregate and individual health probes return 200 for healthy, 206 for
degraded, and 503 for unhealthy, empty, or unknown states. The full component
snapshot returns the usual 200 envelope when it can be encoded. Encoding
failure returns a fixed internal error with the request ID when available.

Component health probes return 503 when the fresh check fails, is canceled,
is superseded, or cannot run after manager closure. A cached healthy result
cannot turn such a probe into a successful response. Missing components remain
404. A component reported as present with no snapshot returns a structured
503. Failure messages omit backend error text. The handler derives the response
from a local value, so an injected manager can retain ownership of its
snapshot. Component names in the list response are sorted lexically.

Overlapping aggregate health checks publish readiness only from the newest batch. Superseded batches return an unhealthy check result without replacing newer readiness; component-level version checks remain independent.

Manual `MarkReady` and `MarkNotReady` decisions invalidate aggregate checks that started before the decision. Those older aggregates cannot replace the manual state or timestamp. A new aggregate started afterward may publish a new decision; manual state is not a permanent override. Both manual operations are ignored after manager closure.
