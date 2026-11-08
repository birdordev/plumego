# x/data/sharding

> **Import path:** `github.com/birdordev/plumego/x/data/sharding` — sub-package of [`x/data`](../README.md).

## Purpose

`x/data/sharding` is a database sharding router that distributes SQL queries
across multiple `x/data/rw.Cluster` shards. It owns shard key resolution, SQL
query rewriting, configurable routing strategies, and cross-shard query policies.

## Status

`experimental` — API not frozen. See [`docs/concepts/extension-maturity.md`](../../../../concepts/extension-maturity.md).

## Use this module when

- you need horizontal partitioning of SQL data across multiple database clusters
- you need shard-key-based query routing with pluggable strategy selection
- you want explicit shard-scoped transactions with safe cross-shard denial

## Do not use this module for

- primary-replica splitting within a single shard — use `x/data/rw`
- storage interface definitions — those live in `store`
- HTTP transport behavior

## Routing strategies

| Strategy | Description |
|---|---|
| `Hash` | Consistent-hash shard key assignment |
| `Mod` | Modulo-based shard assignment |
| `Range` | Range-based shard assignment |
| `List` | Explicit key-to-shard mapping |
| `Custom` | Caller-supplied routing function |

## Cross-shard query policies

| Policy | Behavior |
|---|---|
| `Deny` (default) | Returns error on cross-shard queries |
| `First` | Routes to first shard, ignores others |
| `FirstSuccess` | Routes to first shard that succeeds |

## Validation

```bash
go test -race -timeout 60s ./x/data/sharding/...
go vet ./x/data/sharding/...
```
