# x/gateway/transform

> **Import path:** `github.com/birdordev/plumego/x/gateway/transform` — sub-package of [`x/gateway`](../README.md).

## Purpose

`x/gateway/transform` provides request and response transformation helpers for
the gateway: header injection/removal, query-param edits, JSON body rewriting,
and method/status overrides, composable into transform chains.

## Status

`beta surface` — production-ready with caveats; parent family `x/gateway` is
beta. See [`docs/EXTENSION_MATURITY.md`](../../../../EXTENSION_MATURITY.md).

## Use this module when

- injecting, renaming, or removing request/response headers at the gateway
- adding or removing query parameters
- rewriting or renaming fields in JSON request/response bodies
- overriding request method or response status

## Do not use this module for

- routing decisions — owned by the `x/gateway` proxy
- business-logic transforms
- protocol transcoding — use `x/gateway/protocol`

## Public entrypoints

- `Middleware` — transform middleware
- `Config` — transform configuration
- `RequestTransformer` / `ResponseTransformer` — transform function types
- `ChainRequest` / `ChainResponse` — compose transforms
- `AddRequestHeader`, `RemoveRequestHeader`, `RenameRequestHeader`,
  `AddResponseHeader`, `RemoveResponseHeader`, `RenameResponseHeader` — header transforms
- `AddQueryParam`, `RemoveQueryParam` — query-param transforms
- `ModifyJSONRequest`, `ModifyJSONResponse`, `RenameJSONRequestField`,
  `RenameJSONResponseField` — JSON body transforms
- `SetRequestMethod`, `SetResponseStatus` — method/status overrides

## Validation

```bash
go test -race -timeout 60s ./x/gateway/transform/...
go vet ./x/gateway/transform/...
```
