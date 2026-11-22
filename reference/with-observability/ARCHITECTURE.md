# Architecture Notes — with-observability

This document explains the structural choices in this feature demo. It
extends `reference/standard-service` with `x/observability` and follows the
same layout discipline.

---

## What this demo adds

`reference/standard-service` wires `httpmetrics.Middleware` with a noop
collector. This demo replaces the noop with a real `PrometheusCollector` and
adds `mwtracing.Middleware` with an in-process `OpenTelemetryTracer`. Everything
else is identical to the standard service shape.

```
main.go           signal context and top-level wiring
internal/
  config/         config.go — adds MetricsNamespace, MetricsMaxSeries, ServiceName
  app/            app.go, routes.go, app_test.go
  handler/        api.go (APIHandler, ObservabilityHandler, MetricsHandler)
                  health.go, write.go
```

No domain sub-package is needed — this demo has no domain model.

---

## `app.go` — two additional dependencies

`app.New` constructs `PrometheusCollector` and `OpenTelemetryTracer` alongside
the stable-root dependencies. Both are fields on `App`, not globals:

```go
type App struct {
    Core      *core.App
    Cfg       config.Config
    Collector *observability.PrometheusCollector
    Tracer    *observability.OpenTelemetryTracer
}
```

Both are constructed before the middleware stack so they can be wired in:

```go
collector := observability.NewPrometheusCollector(cfg.App.MetricsNamespace).
    WithMaxMemory(cfg.App.MetricsMaxSeries)
tracer := observability.NewOpenTelemetryTracer(cfg.App.ServiceName)
```

---

## Middleware order

```
requestid → security → httpmetrics → tracing → accesslog → cors → recovery → bodylimit → timeout
```

Relative to `standard-service`, two changes:

- `httpmetrics.Middleware(collector)` receives the real `PrometheusCollector`
  instead of the noop collector.
- `mwtracing.Middleware(tracer)` runs after `httpmetrics` and carries the
  request ID stamped by `requestid`.
- Metrics, tracing, and access logs observe the final status, including CORS
  responses, body-limit rejections, timeouts, and recovered panics.

`timeout` remains innermost so the 504 response is served from within the
timeout boundary.

---

## `routes.go` — three new routes

```
GET /metrics               → Prometheus text exposition (exporter.Handler())
GET /api/v1/stats          → JSON summary via PrometheusCollector.GetStats()
GET /api/v1/collector-stats → JSON summary via metrics.StatsReader interface
GET /api/v1/spans          → OpenTelemetry spans (development only; remove in production)
```

`GET /metrics` is served by `observability.NewPrometheusExporter(collector).Handler()`,
which renders the live Prometheus text format on every request without caching.

The `routeReg` helper (same pattern as `standard-service`) accumulates the first
registration error so the route table remains flat.

---

## `metrics.StatsReader` interface pattern

`MetricsHandler.Observer` is typed as `metrics.StatsReader` rather than the
concrete `*PrometheusCollector`. This demonstrates that any `AggregateCollector`
— noop, `BaseMetricsCollector`, `MultiCollector`, or `PrometheusCollector` —
satisfies the same read interface without changing handler code.

---

## Development inspection vs production export

The in-process `OpenTelemetryTracer` stores spans in memory. `GET /api/v1/spans`
exposes them for development inspection without requiring a tracing backend.

For production, replace `OpenTelemetryTracer` with an OTLP gRPC exporter. The
`mwtracing.Middleware` interface is unchanged; only the constructor call in
`app.New` needs to change. Remove `GET /api/v1/spans` when switching to an
external exporter.

---

## Shutdown sequence

```
main.run signal context cancel
  → a.Core.Shutdown(ctx)   — drain in-flight HTTP requests
```

`PrometheusCollector` and `OpenTelemetryTracer` hold no background goroutines.
Shutdown only requires draining the HTTP server. `app.Start` reacts to the
caller-owned context via `core.App.Shutdown`.

---

## Route table

| Method | Path | Handler | Notes |
|--------|------|---------|-------|
| GET | `/` | `APIHandler.Root` | service identity |
| GET | `/healthz` | `HealthHandler.Live` | liveness probe, always 200 |
| GET | `/readyz` | `HealthHandler.Ready` | readiness probe |
| GET | `/api/hello` | `APIHandler.Hello` | service discovery |
| GET | `/metrics` | `PrometheusExporter.Handler()` | Prometheus text format |
| GET | `/api/v1/stats` | `ObservabilityHandler.Stats` | JSON collector summary |
| GET | `/api/v1/collector-stats` | `MetricsHandler.CollectorStats` | via StatsReader interface |
| GET | `/api/v1/spans` | `ObservabilityHandler.Spans` | dev only; remove in production |

---

## What this demo does not demonstrate

- OTLP gRPC export to an external backend (e.g. Jaeger, Zipkin)
- Metric cardinality control beyond `WithMaxMemory`
- Auth guard on `/metrics` or `/api/v1/spans`
- Multi-collector fan-out with `metrics.MultiCollector`
- Custom span attributes or baggage propagation
