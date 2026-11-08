# x/observability/dbinsights

> **Import path:** `github.com/birdordev/plumego/x/observability/dbinsights` — sub-package of [`x/observability`](../README.md).

## Purpose

`x/observability/dbinsights` provides database query instrumentation: slow-query
detection, query error-rate aggregation, and connection-pool saturation metrics.

## Status

`beta surface` — production-ready with caveats; parent family `x/observability`
is beta. See [`docs/EXTENSION_MATURITY.md`](../../../../EXTENSION_MATURITY.md).

## Use this module when

- detecting and recording slow database queries against a threshold
- aggregating query error rates and per-table/operation stats
- wrapping a DB handle to emit query metrics

## Do not use this module for

- database driver or connection management — use `store/db` and `x/data`
- export backend configuration — use `x/observability`
- business query logic or retry policy

## Public entrypoints

- `Detector` / `NewDetector` — slow-query detector
- `DetectorOption` with `WithThreshold`, `WithMaxRecords`, `WithCallback` — detector options
- `Aggregator` / `NewAggregator` — query stats aggregation
- `Observer` / `NewObserver`, `AggregatingObserver` / `NewAggregatingObserver`,
  `MetricsObserver` — query observers
- `InstrumentedDB` / `NewInstrumentedDB`, `NewSlowQueryDB` — instrumented DB wrappers

## Validation

```bash
go test -race -timeout 60s ./x/observability/dbinsights/...
go vet ./x/observability/dbinsights/...
```
