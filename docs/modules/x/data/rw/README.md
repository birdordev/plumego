# x/data/rw

> **Import path:** `github.com/birdordev/plumego/x/data/rw` — sub-package of [`x/data`](../README.md).

## Purpose

`x/data/rw` provides primary-replica read-write cluster orchestration. It
routes writes to the primary, distributes reads across healthy replicas, and
handles replica health checking, fallback, and per-cluster query metrics.

## Status

`experimental` — API not frozen. See [`docs/concepts/extension-maturity.md`](../../../../concepts/extension-maturity.md).

## Use this module when

- your service uses primary-replica PostgreSQL or MySQL clusters
- you want automatic read routing to healthy replicas
- you need transaction-aware routing (transactions always go to primary)
- you want fallback-to-primary when all replicas are unhealthy

## Do not use this module for

- horizontal sharding across multiple databases — use `x/data/sharding`
- storage interface definitions — those live in `store`
- HTTP transport behavior

## Notes

- Writes and transactions are always routed to the primary.
- Replica health is checked on a configurable interval; unhealthy replicas are
  removed from the read pool until they recover.
- `FallbackToPrimary` mode serves reads from primary when all replicas are down,
  preventing read outages at the cost of primary load.

## Validation

```bash
go test -race -timeout 60s ./x/data/rw/...
go vet ./x/data/rw/...
```
