# x/ai/resilience

> **Import path:** `github.com/birdordev/plumego/x/ai/resilience` — sub-package of [`x/ai`](../README.md).

## Purpose

`x/ai/resilience` is a resilience decorator for AI providers. It composes
`x/resilience` circuit-breaker and rate-limit primitives into a
`provider.Provider`-compatible wrapper with per-provider/model rate-limit keys
and breaker state exposure.

## Status

`experimental surface` — APIs may change; parent family `x/ai` is experimental.
See [`docs/EXTENSION_MATURITY.md`](../../../../EXTENSION_MATURITY.md).

## Use this module when

- wrapping an LLM provider with circuit-breaking and rate-limiting
- deriving rate-limit keys per provider and model
- inspecting circuit breaker state and stats from caller code

## Do not use this module for

- defining resilience primitives — owned by `x/resilience`
- provider adapter implementation or session lifecycle
- feature-specific retry or orchestration policy — keep caller-owned

## Public entrypoints

- `ResilientProvider` — provider decorator
- `Config` — wiring for `x/resilience` rate-limit and circuit-breaker inputs
- `NewResilientProvider` — constructor returning an error
- `ErrNilProvider`, `ErrNilRequest`, `ErrRateLimitExceeded` — sentinel errors

## Validation

```bash
go test -race -timeout 60s ./x/ai/resilience/...
go test -timeout 20s ./x/ai/resilience/...
go vet ./x/ai/resilience/...
```
