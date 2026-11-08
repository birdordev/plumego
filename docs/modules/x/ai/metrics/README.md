# x/ai/metrics

> **Import path:** `github.com/birdordev/plumego/x/ai/metrics` — sub-package of [`x/ai`](../README.md).

## Purpose

`x/ai/metrics` defines an AI-oriented metrics `Collector` interface with
in-process implementations and an adapter that bridges to the stable `metrics`
layer. It supplies the metric sink consumed by `x/ai/instrumentation`.

## Status

`experimental surface` — APIs may change; parent family `x/ai` is experimental.
See [`docs/EXTENSION_MATURITY.md`](../../../../EXTENSION_MATURITY.md).

## Use this module when

- emitting AI-specific counters, gauges, histograms, and timings
- bridging AI metrics to a stable-layer collector via `AggregateCollectorAdapter`
- using `MemoryCollector` or `NoOpCollector` in tests

## Do not use this module for

- replacing or duplicating the stable `metrics` package
- HTTP metrics middleware — use `middleware/httpmetrics`
- observability export wiring — use `x/observability`

## Public entrypoints

- `Collector` — AI-oriented metric collector interface
- `Tag` — tag model for AI metrics
- `TimerContext` — timing helper
- `MemoryCollector` / `NewMemoryCollector` — in-process collector
- `NoOpCollector` — no-op collector for tests
- `AggregateCollectorAdapter` — bridge to stable `metrics.AggregateCollector`

## Validation

```bash
go test -race -timeout 60s ./x/ai/metrics/...
go test -timeout 20s ./x/ai/metrics/...
go vet ./x/ai/metrics/...
```
