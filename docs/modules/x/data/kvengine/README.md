# x/data/kvengine

> **Import path:** `github.com/birdordev/plumego/x/data/kvengine` — sub-package of [`x/data`](../README.md).

## Purpose

`x/data/kvengine` is a durable embedded KV engine with WAL, snapshots,
serializer selection (binary/JSON/auto-detect), compression, and engine-level
tuning. It owns everything that belongs above the stable `store/kv` primitive.

## Status

`experimental` — API not frozen. See [`docs/concepts/extension-maturity.md`](../../../../concepts/extension-maturity.md).

## Use this module when

- you need WAL-backed durability beyond the in-process `store/kv` store
- you need snapshot and replay capability
- you need configurable serialization format or compression
- you need per-shard tuning or flush-interval control

## Do not use this module for

- simple in-process key-value caching — use `store/kv`
- tenant-aware KV scoping — keep that in application code
- HTTP transport behavior

## Entry points

| Symbol | Purpose |
|---|---|
| `KVStore` | Main KV engine type |
| `NewKVStore` | Constructor; returns error on invalid config |
| `Options` | Engine configuration (shards, WAL sync mode, serializer, etc.) |
| `WALEntry`, `WALSyncMode` | WAL configuration types |
| `Serializer`, `BinarySerializer`, `JSONSerializer` | Serialization format types |

## Validation

```bash
go test -race -timeout 60s ./x/data/kvengine/...
go vet ./x/data/kvengine/...
```
