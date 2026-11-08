# x/ai/semanticcache/vectorstore

> **Import path:** `github.com/birdordev/plumego/x/ai/semanticcache/vectorstore` — sub-package of [`x/ai/semanticcache`](../README.md).

## Purpose

`x/ai/semanticcache/vectorstore` defines the vector storage backend interface
and in-memory implementations (`MemoryVectorStore` with LRU/TTL eviction, and a
layered `TieredVectorStore`) used for semantic similarity search.

## Status

`experimental surface` — APIs may change; parent family `x/ai` is experimental.
See [`docs/EXTENSION_MATURITY.md`](../../../../../EXTENSION_MATURITY.md).

## Use this module when

- storing and searching embedding vectors for the semantic cache
- needing in-memory vector storage with LRU eviction and TTL expiry
- layering hot/cold vector storage tiers

## Do not use this module for

- embedding generation — use `x/ai/semanticcache/embedding`
- cache lifecycle management and warm-up — use `x/ai/semanticcache/cachemanager`
- persistent or distributed vector databases — supply your own via `Backend`
- HTTP endpoints or query APIs

## Public entrypoints

- `Backend` — vector storage interface (Add, Search, Delete, Clear, Stats)
- `VectorEntry` — stored vector value type
- `SimilarityResult` — search result value type
- `MemoryVectorStore` / `NewMemoryVectorStore` — in-memory store with LRU/TTL
- `TieredVectorStore` / `NewTieredVectorStore` — hot/cold layered store

## Validation

```bash
go test -race -timeout 60s ./x/ai/semanticcache/vectorstore/...
go test -timeout 20s ./x/ai/semanticcache/vectorstore/...
go vet ./x/ai/semanticcache/vectorstore/...
```
