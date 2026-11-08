# x/data/migrate

> **Import path:** `github.com/birdordev/plumego/x/data/migrate` — sub-package of [`x/data`](../README.md).

## Purpose

`x/data/migrate` is a dependency-free database migration runner contract for
`x/data` callers. It wraps migration execution, up/down control, and status
projection without pulling in a third-party migration framework.

## Status

`experimental` — API not frozen. See [`docs/concepts/extension-maturity.md`](../../../../concepts/extension-maturity.md).

## Use this module when

- you need to run SQL schema migrations from within application startup
- you want a consistent `up`, `down`, and `status` API over your migration files
- you want to keep migration wiring inside `x/data` without adding external
  migration library dependencies to the stable store layer

## Do not use this module for

- schema design or migration file authoring — keep that in application code
- HTTP endpoints for migration control — expose that explicitly in your admin routes

## Validation

```bash
go test -race -timeout 60s ./x/data/migrate/...
go vet ./x/data/migrate/...
```
