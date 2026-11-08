# x/data/sqlx

> **Import path:** `github.com/birdordev/plumego/x/data/sqlx` — sub-package of [`x/data`](../README.md).

## Purpose

`x/data/sqlx` provides `database/sql`-backed query and transaction adapter
methods for caller-owned `*sql.DB` instances. It wraps standard library SQL
with explicit context propagation and Plumego-compatible error handling.

## Status

`experimental` — API not frozen. See [`docs/concepts/extension-maturity.md`](../../../../concepts/extension-maturity.md).

## Use this module when

- your application uses `database/sql` as its database driver
- you want explicit context propagation on every SQL operation
- you want a thin adapter between `database/sql` and `store/db` caller patterns

## Do not use this module for

- ORM behavior or query building — keep that in application code
- PostgreSQL-specific features — use `x/data/pgx`
- changing `store/db` interface definitions

## Notes

- Caller owns `*sql.DB` lifecycle; this package wraps, never owns, the DB handle.
- All operations require a caller-supplied `context.Context`.

## Validation

```bash
go test -race -timeout 60s ./x/data/sqlx/...
go vet ./x/data/sqlx/...
```
