# x/data/idempotency

> **Import path:** `github.com/birdordev/plumego/x/data/idempotency` — sub-package of [`x/data`](../README.md).

## Purpose

`x/data/idempotency` provides durable idempotency provider implementations
behind the stable `store/idempotency` contract. It owns SQL-backed and KV-backed
providers along with duplicate-key deduplication policy.

## Status

`experimental` — API not frozen. See [`docs/concepts/extension-maturity.md`](../../../../concepts/extension-maturity.md).  
`x/data/idempotency` is a beta surface at v1.1.0: API unchanged across v1.0.0 → v1.1.0, owner sign-off recorded.

## Use this module when

- you need durable (cross-restart) idempotency beyond the in-memory `store/idempotency` store
- you want SQL-backed idempotency keys with configurable table policy
- you want KV-engine-backed idempotency keys for non-SQL deployments

## Do not use this module for

- business-specific deduplication rules — keep those in application code
- `store/idempotency` interface changes
- HTTP transport behavior

## Entry points

| Symbol | Purpose |
|---|---|
| `SQLStore` | Durable SQL-backed idempotency store |
| `SQLConfig` | SQL store configuration (table name, dialect) |

## Validation

```bash
go test -race -timeout 60s ./x/data/idempotency/...
go vet ./x/data/idempotency/...
```
