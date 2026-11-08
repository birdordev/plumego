# x/observability/windowmetrics

> **Import path:** `github.com/birdordev/plumego/x/observability/windowmetrics` — sub-package of [`x/observability`](../README.md).

## Purpose

`x/observability/windowmetrics` is a thread-safe sliding-window metric aggregator
producing rolling rate and latency summaries (count, sum, p50/p95/p99).

## Status

`beta surface` — production-ready with caveats; parent family `x/observability`
is beta. See [`docs/EXTENSION_MATURITY.md`](../../../../EXTENSION_MATURITY.md).

## Use this module when

- computing rolling rate or latency percentiles over a sliding time window
- needing thread-safe window rotation for live metric summaries

## Do not use this module for

- export or scrape endpoint ownership — use `x/observability`
- business freshness or retention policy

## Public entrypoints

- `Aggregator` / `NewAggregator` — sliding-window aggregator
- `AggregatorStats` — window summary value type (count, sum, percentiles)

## Validation

```bash
go test -race -timeout 60s ./x/observability/windowmetrics/...
go vet ./x/observability/windowmetrics/...
```
