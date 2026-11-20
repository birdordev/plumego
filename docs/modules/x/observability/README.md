# x/observability

## Purpose

`x/observability` is the app-facing extension root for broader observability adapters and export wiring.

## v1 Status

- `beta` in the Plumego v1 support matrix
- Included in repository release scope with beta compatibility obligations
- Promoted at `v0.2.0` after release-backed evidence showed no exported
  `x/observability/*` API changes across refs `d2c25c3` and `ec70358`, with
  `observability` owner sign-off recorded in
  `docs/evidence/extension/x-observability.md`

## Use this module when

- the task is exporter or adapter integration work
- the task is broader diagnostics or telemetry pipeline wiring
- the task is moving non-stable observability helpers or metrics test utilities out of stable roots

## Do not use this module for

- transport-only middleware primitives
- application bootstrap
- feature-specific business metrics policy

## First files to read

- `x/observability/module.yaml`
- the owning package under `x/observability/*`
- `docs/modules/middleware/README.md`

## Canonical change shape

- keep export wiring explicit
- keep adapter-local behavior reviewable
- keep transport observability primitives in stable `middleware/*`
- keep buffered metric-record inspection in `x/observability/recordbuffer`
- keep rolling-window aggregation helpers in `x/observability/windowmetrics`
- keep metrics test utilities in `x/observability/testmetrics`
- keep DB analytics and slow-query helpers in `x/observability/dbinsights`

## Boundary rules

- `x/observability` is for exporter, adapter, and pipeline wiring only; keep transport-layer observation (per-request counters, latency) in stable `middleware/httpmetrics`
- do not add hidden global collectors or automatic registration at import time; pass `Hooks` and config explicitly via `Configure`
- keep `PrometheusCollector` and `PrometheusExporter` isolated per service instance; do not share collectors across test cases or goroutines without coordination
- the exported `*_http_requests_total_all` counter includes evicted label
  series and resets on `Clear()`. `GetStats()` reports the resident HTTP
  series alongside the base collector statistics.
- use the error-returning constructors `NewPrometheusExporter`,
  `tracer.NewProbabilitySampler`, and `tracer.NewTracer` for dynamic wiring
- keep `OpenTelemetryTracer` scoped per service; `Clear()` and `WithMaxSpans()` exist for bounded test use, not for production hot-path eviction
- `OpenTelemetryTracer` stores W3C-shaped trace/span identifiers in
  `contract.TraceContext`; invalid inbound `X-Trace-ID` values may be recorded
  as diagnostic parent attributes but must not become propagation trace IDs
- DB insight helpers (`dbinsights`) are analytics utilities only; do not use them as a query-layer abstraction or replace stable `store` APIs

## Production Observability Profile

Keep the production stack split by ownership:

1. Stable `middleware/requestid`, `middleware/tracing`, `middleware/httpmetrics`, and `middleware/accesslog` collect request-scoped transport signals.
2. `x/observability` owns collector/exporter instances, tracing adapters, record buffers, test metrics, and DB insight helpers.
3. `x/observability/ops` exposes protected admin and diagnostics routes when an operator surface is required.
4. `x/observability/devtools` remains local-development or protected-environment tooling and should not be mounted by default in production.

`reference/production-service` demonstrates this split without creating a
hidden observability bundle: health routes are mounted as app-local probe
handlers, request metrics are collected by stable middleware, `/ops/metrics` is
protected by `OPS_TOKEN`, and `x/observability/devtools` is left unmounted.

Metrics and health exposure are deployment-policy decisions. Public liveness and
readiness routes are acceptable for many load balancer setups, while metrics,
admin, diagnostics, and debug routes should have an explicit access boundary
when they expose runtime internals.

Health HTTP orchestration belongs in `x/observability/ops/healthhttp` or app-local wiring.
The stable `health` root owns readiness models and helpers, not HTTP handler
ownership.

`windowmetrics.Aggregator.GetAllStats` reports metric names that have samples
in the window. Recording new samples also reclaims idle names in bounded
batches.

## Current test coverage

- `PrometheusCollector`: observe/handler round-trip, multiple requests with different labels, stats (ActiveSeries, TotalRecords, NameBreakdown, StartTime), Clear, WithMaxMemory eviction, concurrency, Prometheus text format and label escaping (newline and quote injection), empty namespace default, zero-max-memory fallback, eviction of least-used series
- `PrometheusExporter.Handler()`: output format (all metric names, label values), Content-Type header (`text/plain` prefix), empty-collector 200 response with uptime line
- `OpenTelemetryTracer`: empty-name fallback, span lifecycle (Start/End), 4xx/5xx/success span classification, span attributes completeness, parent trace-ID propagation, `GetSpanStats`, `Clear`, `WithMaxSpans` bounding
- `Configure`: both metrics and tracing enabled, custom namespace, custom service name, concurrent calls, max-series, custom path, mutable-callback invocation
- Subpackages: `recordbuffer`, `windowmetrics`, `testmetrics`, `testlog`, `tracer`, `featuremetrics`, `dbinsights` — each has dedicated unit tests

## Beta readiness

`x/observability` satisfies the current coverage and boundary portions of
`docs/reference/extension-stability-policy.md`: collector/exporter behavior, tracer
lifecycle, configuration, and supporting record-buffer, window, test, feature,
and DB insight packages have focused tests.

The module is beta. The beta evidence in
`docs/evidence/extension/x-observability.md` records two release refs, matching
API snapshots, no exported API changes, and `observability` owner sign-off.
Transport observability primitives remain in stable `middleware/*`; exporter
and adapter wiring stays here.
