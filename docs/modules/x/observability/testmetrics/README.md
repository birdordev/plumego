# x/observability/testmetrics

> **Import path:** `github.com/birdordev/plumego/x/observability/testmetrics` — sub-package of [`x/observability`](../README.md).

## Purpose

`x/observability/testmetrics` is a test-only mock implementation of
`metrics.AggregateCollector` that records emitted metrics so tests can assert on
them.

## Status

`beta surface` — production-ready with caveats; parent family `x/observability`
is beta. See [`docs/EXTENSION_MATURITY.md`](../../../../EXTENSION_MATURITY.md).

## Use this module when

- asserting metric emissions in unit and integration tests
- inspecting recorded counts and last values per subsystem

## Do not use this module for

- production metric collection — this is a test-support package, not for app wiring
- metric export or scrape functionality

## Public entrypoints

- `NewMockCollector` — construct a recording mock collector
- `MockCollector` — in-memory `metrics.AggregateCollector` mock
- `HTTPCall`, `DBCall`, `KVCall`, `MQCall`, `PubSubCall`, `IPCCall` — recorded call value types

## Validation

```bash
go test -race -timeout 60s ./x/observability/testmetrics/...
go vet ./x/observability/testmetrics/...
```
