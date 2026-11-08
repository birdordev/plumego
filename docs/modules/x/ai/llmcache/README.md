# x/ai/llmcache

> **Import path:** `github.com/birdordev/plumego/x/ai/llmcache` — sub-package of [`x/ai`](../README.md).

## Purpose

`x/ai/llmcache` defines the LLM response caching interface and an in-memory
implementation, plus a `CachingProvider` wrapper for a transparent cache-aside
pattern. It is the exact-key caching layer consumed by `x/ai/semanticcache` and
`x/ai/instrumentation`.

## Status

`experimental surface` — APIs may change; parent family `x/ai` is experimental.
See [`docs/EXTENSION_MATURITY.md`](../../../../EXTENSION_MATURITY.md).

## Use this module when

- caching LLM responses by exact prompt/model key
- wrapping a provider with cache-aside behavior via `CachingProvider`
- using `MemoryCache` for local dev and unit tests

## Do not use this module for

- semantic / embedding-based matching — use `x/ai/semanticcache`
- distributed or persistent cache backends — use `x/data/cache`
- HTTP response caching — use `x/gateway/cache`

## Public entrypoints

- `Cache` — cache interface (Get, Set, Delete, Clear)
- `CacheKey` — deterministic key from prompt hash and model identity
- `CacheEntry` — value type with TTL and hit/miss metadata
- `MemoryCache` / `NewMemoryCache` — in-memory implementation
- `CachingProvider` / `NewCachingProvider` — cache-aside provider wrapper

## Validation

```bash
go test -race -timeout 60s ./x/ai/llmcache/...
go test -timeout 20s ./x/ai/llmcache/...
go vet ./x/ai/llmcache/...
```
