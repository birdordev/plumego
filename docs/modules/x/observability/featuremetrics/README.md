# x/observability/featuremetrics

> **Import path:** `github.com/birdordev/plumego/x/observability/featuremetrics` — sub-package of [`x/observability`](../README.md).

## Purpose

`x/observability/featuremetrics` provides helpers for recording feature-level
metrics — adoption rates and error counts per subsystem (DB, KV, MQ, PubSub,
IPC) — with tag helpers.

## Status

`beta surface` — production-ready with caveats; parent family `x/observability`
is beta. See [`docs/EXTENSION_MATURITY.md`](../../../../EXTENSION_MATURITY.md).

## Use this module when

- recording per-feature counters and error rates
- emitting duration metrics for DB, KV, MQ, PubSub, or IPC operations

## Do not use this module for

- feature flag evaluation — this package holds no flag storage or decision logic
- export backend ownership — use `x/observability`

## Public entrypoints

- `ObserveDB`, `ObserveKV`, `ObserveMQ`, `ObservePubSub`, `ObserveIPC` — per-subsystem observers
- `DBRecord`, `KVRecord`, `MQRecord`, `PubSubRecord`, `IPCRecord` — record helpers
- `ExtractTableName` — table-name tag helper

## Validation

```bash
go test -race -timeout 60s ./x/observability/featuremetrics/...
go vet ./x/observability/featuremetrics/...
```
