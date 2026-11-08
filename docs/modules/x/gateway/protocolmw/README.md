# x/gateway/protocolmw

> **Import path:** `github.com/birdordev/plumego/x/gateway/protocolmw` — sub-package of [`x/gateway`](../README.md).

## Purpose

`x/gateway/protocolmw` provides protocol-aware middleware for the gateway proxy
pipeline, applying protocol-specific request/response transforms by delegating to
registered `x/gateway/protocol` adapters.

## Status

`beta surface` — production-ready with caveats; parent family `x/gateway` is
beta. See [`docs/EXTENSION_MATURITY.md`](../../../../EXTENSION_MATURITY.md).

## Use this module when

- inserting protocol-specific transforms into a gateway middleware chain
- routing requests through a protocol adapter registry inside the proxy pipeline

## Do not use this module for

- generic HTTP middleware — use the `middleware` stable root
- protocol adapter logic itself — use `x/gateway/protocol`
- business policy enforcement

## Public entrypoints

- `Middleware` — protocol-aware gateway middleware
- `MiddlewareWithConfig` — middleware with explicit configuration
- `Config` — middleware configuration

## Validation

```bash
go test -race -timeout 60s ./x/gateway/protocolmw/...
go vet ./x/gateway/protocolmw/...
```
