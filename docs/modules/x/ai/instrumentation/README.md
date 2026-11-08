# x/ai/instrumentation

> **Import path:** `github.com/birdordev/plumego/x/ai/instrumentation` — sub-package of [`x/ai`](../README.md).

## Purpose

`x/ai/instrumentation` provides decorator wrappers that add metrics collection
to `x/ai` components (provider, cache, orchestration engine, and steps) without
modifying the wrapped types. Timing and counters are emitted via
`x/ai/metrics.Collector`.

## Status

`experimental surface` — APIs may change; parent family `x/ai` is experimental.
See [`docs/EXTENSION_MATURITY.md`](../../../../EXTENSION_MATURITY.md).

## Use this module when

- adding metrics to a provider, cache, engine, or step without changing it
- composing instrumentation at the handler or app-wiring layer
- emitting AI timing and counter metrics through a single collector

## Do not use this module for

- distributed tracing export — use `x/observability`
- log emission or structured event routing
- metric backend wiring — use `x/ai/metrics.AggregateCollectorAdapter`

## Public entrypoints

- `InstrumentedProvider` / `NewInstrumentedProvider` — wraps `provider.Provider`
- `InstrumentedCache` / `NewInstrumentedCache` — wraps `llmcache.Cache`
- `InstrumentedEngine` / `NewInstrumentedEngine` — wraps `orchestration.Engine`
- `InstrumentedStep` / `NewInstrumentedStep` — wraps an orchestration step

## Validation

```bash
go test -race -timeout 60s ./x/ai/instrumentation/...
go test -timeout 20s ./x/ai/instrumentation/...
go vet ./x/ai/instrumentation/...
```
