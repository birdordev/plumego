# x/ai/semanticcache

> **Import path:** `github.com/birdordev/plumego/x/ai/semanticcache` — sub-package of [`x/ai`](../README.md).

## Purpose

`x/ai/semanticcache` is an embedding-based semantic cache for AI provider
responses. It matches requests by vector similarity, exposes a
`provider.Provider` decorator, and can fall back to exact-key caching via
`x/ai/llmcache`. Embedding, vector storage, and lifecycle are delegated to its
subordinate packages.

## Status

`experimental surface` — APIs may change; parent family `x/ai` is experimental.
See [`docs/EXTENSION_MATURITY.md`](../../../../EXTENSION_MATURITY.md).

## Use this module when

- caching provider responses by fuzzy prompt similarity rather than exact key
- wrapping a provider with a semantic cache-aside decorator
- bridging caller-provided embedding and vector-store backends into a cache

## Do not use this module for

- exact-key caching — use `x/ai/llmcache`
- distributed or persistent cache backends — use `x/data/cache`
- embedding generation — use [`x/ai/semanticcache/embedding`](embedding/README.md)
- vector storage — use [`x/ai/semanticcache/vectorstore`](vectorstore/README.md)
- cache lifecycle / warm-up — use [`x/ai/semanticcache/cachemanager`](cachemanager/README.md)

## Public entrypoints

- `SemanticCache`, `SemanticCacheConfig`, `SemanticCacheStats`, `DefaultSemanticCacheConfig`, `NewSemanticCache` — similarity-lookup cache
- `SemanticCachingProvider`, `ProviderConfig`, `DefaultProviderConfig`, `NewSemanticCachingProvider` — provider decorator
- `EmbeddingGenerator` — embedding bridge interface
- `VectorStore`, `VectorEntry`, `SimilarityResult` — vector-store bridge contracts
- `MetricRecorder`, `MetricRecord`, `NoopMetricRecorder` — metric hooks
- `InstrumentedSemanticCache` / `NewInstrumentedSemanticCache`, `InstrumentedVectorStore` / `NewInstrumentedVectorStore` — instrumented wrappers
- `ErrProviderRequired`, `ErrSemanticCacheRequired` — sentinel errors

## Validation

```bash
go test -race -timeout 60s ./x/ai/semanticcache/...
go test -timeout 20s ./x/ai/semanticcache/...
go vet ./x/ai/semanticcache/...
```
