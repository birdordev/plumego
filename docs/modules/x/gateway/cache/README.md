# x/gateway/cache

> **Import path:** `github.com/birdordev/plumego/x/gateway/cache` — sub-package of [`x/gateway`](../README.md).

## Purpose

`x/gateway/cache` provides an HTTP response cache for the gateway reverse proxy,
keyed by request method, URL, and configurable headers, with pluggable key
strategies and an in-memory store.

## Status

`beta surface` — production-ready with caveats; parent family `x/gateway` is
beta. See [`docs/EXTENSION_MATURITY.md`](../../../../EXTENSION_MATURITY.md).

## Use this module when

- caching HTTP responses in front of gateway upstreams
- choosing a cache key strategy (URL-only, header-aware, tenant- or user-aware)
- backing the cache with the in-memory store

## Do not use this module for

- distributed or Redis cache storage — use `x/data/cache`
- in-process primitive caching — use `store/cache`
- business freshness or TTL policy decisions

## Public entrypoints

- `Middleware` — gateway cache middleware
- `Config` — cache configuration
- `Store` / `MemoryStore` / `NewMemoryStore` — cache store interface and in-memory impl
- `KeyStrategy` — cache key strategy interface
- `NewDefaultKeyStrategy`, `NewURLOnlyKeyStrategy`, `NewHeaderAwareKeyStrategy`,
  `NewQueryParamKeyStrategy`, `NewPathPatternKeyStrategy`,
  `NewCompositeKeyStrategy`, `NewTenantAwareKeyStrategy`,
  `NewUserAwareKeyStrategy`, `NewNoCacheKeyStrategy` — key strategy constructors

## Validation

```bash
go test -race -timeout 60s ./x/gateway/cache/...
go vet ./x/gateway/cache/...
```
