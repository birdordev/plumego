# metrics

## Purpose

`metrics` holds stable metrics contracts and the small in-memory collectors that
other modules can depend on safely.

## v1 Status

- `ga` in the Plumego v1 support matrix
- Public compatibility is expected for the stable package surface

## Use this module when

- changing collector contracts
- adding base collectors or aggregate collector composition
- wiring stable instrumentation against `Recorder` or `HTTPObserver`

## Do not use this module for

- Prometheus or tracing implementations
- dev-only dashboard collectors
- feature-specific metrics reporters or exporters
- rolling-window aggregation helpers
- repo-wide metrics test helpers
- app bootstrap

## First files to read

- `metrics/module.yaml`
- `metrics/collector.go`
- `metrics/multi.go`
- owning extension docs when the change is implementation-specific

## Canonical change shape

- keep collector APIs small
- keep base collectors generic and transport-agnostic
- keep only aggregate collector composition in stable `metrics`
- keep aggregate fan-out nil-safe (filter nil interfaces and collector pointers, return a no-op when no
  collectors are provided, and make empty fan-out methods no-ops)
- keep `AggregateCollector` limited to `Record`, shared `ObserveHTTP`, stats, and reset semantics
- use `NewHTTPRecord(...)` when owner-side collectors need the stable HTTP
  record shape and timestamp; do not encode response byte counts as labels
- classify HTTP status codes `>= 400` as error records in stable base stats;
  explicit `MetricRecord.Error` values remain error records for all metric names
- do not retain per-record buffers inside stable collectors; record inspection belongs in `x/observability/recordbuffer`
- keep metric identity canonical as `MetricRecord.Name`; use `Labels` for dimensions instead of parallel type catalogs
- keep feature-specific observer interfaces in their owning package; only the shared HTTP observer stays in stable `metrics`
- keep non-HTTP feature helper record builders in owning extensions or `x/observability` helper packages
- keep Prometheus and tracing adapters in `x/observability`
- keep record-buffer inspection and retention tuning in `x/observability/recordbuffer`
- keep rolling-window aggregation in `x/observability/windowmetrics`
- keep metrics test helpers in `x/observability/testmetrics`
- keep dev-only collectors in `x/observability/devtools`
- keep feature-specific metrics ownership in the owning extension
- when an extension already owns a richer metrics facade, bridge it into stable
  `AggregateCollector` with an explicit adapter instead of widening stable
  contracts; `x/ai/metrics.NewAggregateCollectorAdapter` is the reference path

## Stable behavior notes

- `BaseMetricsCollector` keeps aggregate counts and name breakdowns only; it does
  not store raw records or normalize generic record timestamps/labels.
- `CollectorStats.NameBreakdown` snapshots are caller-owned maps, including empty
  base/no-op snapshots; empty/no-op stats keep zero start time while base
  collectors set start time on construction and clear.
- `ObserveHTTP(...)` records duration in seconds through the canonical HTTP
  record shape, and `NewHTTPRecord(...)` assigns the record timestamp. Response
  bytes remain available to collectors through the observer method argument, but
  stable base stats do not turn bytes into labels.
- `NewMultiCollector(...)` is the only stable fan-out helper: nil inputs are
  ignored, empty internal slots are skipped, zero targets return a no-op, one target
  is returned unchanged, and multiple targets fan out in order.
- Each target of a multi-target `Record` call receives its own labels map, so
  child mutation cannot affect the caller or a sibling collector. Callers must
  keep input labels stable until dispatch returns; retained error values remain
  caller-owned. Single-target construction still returns the target unchanged.
- Base collector updates, resets, and snapshots share one lock boundary. Stats
  maps are caller-owned. Multi-collector stats combine individual child
  snapshots; they are not a globally atomic snapshot or reset across children.
- Nil base and fan-out collector method calls are safe no-ops; nil stats readers
  return initialized empty stats.
- Multi collectors sum child-maintained active series; if a child omits active
  series but returns a name breakdown, the child breakdown size is used.
- `x/ai/metrics.NewAggregateCollectorAdapter(...)` lets AI instrumentation emit
  into the stable collector contract by translating AI metric calls into stable
  `MetricRecord` events; metric kind is not preserved as a separate stable field,
  so callers should keep names distinct when they need different semantic series.
