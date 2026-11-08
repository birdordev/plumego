# x/observability/recordbuffer

> **Import path:** `github.com/birdordev/plumego/x/observability/recordbuffer` — sub-package of [`x/observability`](../README.md).

## Purpose

`x/observability/recordbuffer` is a bounded in-process metric record buffer used
internally by the `x/observability` export pipeline to queue records for deferred
export and replay.

## Status

`beta surface` — production-ready with caveats; parent family `x/observability`
is beta. See [`docs/EXTENSION_MATURITY.md`](../../../../EXTENSION_MATURITY.md).

## Use this module when

- buffering metric records in memory for deferred export
- replaying buffered records to an export backend

## Do not use this module for

- export backend configuration — use `x/observability`
- distributed record storage
- business metric policy

## Public entrypoints

- `Collector` / `NewCollector` — bounded in-memory record buffer collector
- `WithMaxRecords` — buffer capacity option

## Validation

```bash
go test -race -timeout 60s ./x/observability/recordbuffer/...
go vet ./x/observability/recordbuffer/...
```
