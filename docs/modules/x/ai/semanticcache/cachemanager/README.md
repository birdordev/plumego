# x/ai/semanticcache/cachemanager

> **Import path:** `github.com/birdordev/plumego/x/ai/semanticcache/cachemanager` — sub-package of [`x/ai/semanticcache`](../README.md).

## Purpose

`x/ai/semanticcache/cachemanager` orchestrates cache lifecycle over a
`vectorstore.Backend`: cache warming from a corpus, eviction coordination, and
statistics aggregation.

## Status

`experimental surface` — APIs may change; parent family `x/ai` is experimental.
See [`docs/EXTENSION_MATURITY.md`](../../../../../EXTENSION_MATURITY.md).

## Use this module when

- pre-populating a vector store before serving live traffic
- coordinating eviction and maintenance over a vector-store backend
- aggregating cache statistics from backend stats

## Do not use this module for

- vector storage implementation — use [`x/ai/semanticcache/vectorstore`](../vectorstore/README.md)
- embedding generation — use [`x/ai/semanticcache/embedding`](../embedding/README.md)
- similarity matching logic — owned by parent [`x/ai/semanticcache`](../README.md)
- distributed cache coordination or replication

## Public entrypoints

- `Manager` / `NewManager` — cache lifecycle over `vectorstore.Backend`
- `CacheStats` — aggregated backend statistics
- `Warmer` — pre-populates the vector store from a corpus
- `ErrUnsupportedMaintenance` — sentinel error

## Validation

```bash
go test -race -timeout 60s ./x/ai/semanticcache/cachemanager/...
go test -timeout 20s ./x/ai/semanticcache/cachemanager/...
go vet ./x/ai/semanticcache/cachemanager/...
```
