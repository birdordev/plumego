# x/observability/testlog

> **Import path:** `github.com/birdordev/plumego/x/observability/testlog` — sub-package of [`x/observability`](../README.md).

## Purpose

`x/observability/testlog` is a test-only in-memory implementation of
`log.StructuredLogger` that captures log entries so tests can assert on log
output.

## Status

`beta surface` — production-ready with caveats; parent family `x/observability`
is beta. See [`docs/EXTENSION_MATURITY.md`](../../../../EXTENSION_MATURITY.md).

## Use this module when

- asserting log output in unit and integration tests
- capturing structured log entries in-memory for inspection

## Do not use this module for

- production log output — this is a test-support package, not for app wiring
- log export or transport functionality

## Public entrypoints

- `New` — construct an in-memory test logger
- `Logger` — in-memory `log.StructuredLogger` implementation with capture helpers
- `Entry` — captured log entry value type

## Validation

```bash
go test -race -timeout 60s ./x/observability/testlog/...
go vet ./x/observability/testlog/...
```
