# x/data/pgx

> **Import path:** `github.com/birdordev/plumego/x/data/pgx` — sub-package of [`x/data`](../README.md).

## Purpose

`x/data/pgx` provides PostgreSQL-style query and transaction adapter methods
for caller-owned pools. It wraps pgx pool types with explicit context
propagation and Plumego-compatible error handling.

## Status

`experimental` — API not frozen. See [`docs/concepts/extension-maturity.md`](../../../../concepts/extension-maturity.md).

## Use this module when

- your application uses pgx as its PostgreSQL driver
- you want explicit context propagation on every pool operation
- you want a thin adapter between pgx and `store/db` caller patterns

## Do not use this module for

- ORM behavior or query building — keep that in application code
- live database test dependencies in unit tests
- changing `store/db` interface definitions

## Notes

- All operations require a caller-supplied `context.Context`; the package does
  not hold any implicit connection lifecycle.
- Caller owns pool lifecycle; this package wraps, never owns, the pool.

## Validation

```bash
go test -race -timeout 60s ./x/data/pgx/...
go vet ./x/data/pgx/...
```
