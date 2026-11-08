# x/gateway/protocol

> **Import path:** `github.com/birdordev/plumego/x/gateway/protocol` — sub-package of [`x/gateway`](../README.md).

## Purpose

`x/gateway/protocol` is the protocol adapter layer for the gateway: it bridges
the HTTP/1.1 reverse proxy to gRPC and GraphQL upstream backends via a registry
of protocol adapters.

## Status

`beta surface` — production-ready with caveats; parent family `x/gateway` is
beta. See [`docs/EXTENSION_MATURITY.md`](../../../../EXTENSION_MATURITY.md).

## Use this module when

- transcoding HTTP requests to gRPC upstreams
- forwarding GraphQL queries and introspection through the gateway
- registering and selecting protocol adapters via a `Registry`

## Do not use this module for

- gRPC server or client pool ownership — use `x/rpc`
- business GraphQL schema definition
- authentication — use `middleware/auth` and `security`

## Public entrypoints

- `ProtocolAdapter` — protocol adapter interface
- `Registry` / `NewRegistry` — adapter registry and constructor
- `Request` / `Response` / `ResponseWriter` — adapter transport types
- `GRPCAdapter`, `GRPCRequest`, `GRPCResponse`, `GRPCInvoker`, `GRPCMetadata`,
  `GRPCErrorCode` — HTTP-to-gRPC transcoding
- `GraphQLAdapter`, `GraphQLExecutor`, `GraphQLRequest`, `GraphQLResponse`,
  `GraphQLError`, `GraphQLLocation`, `GraphQLDirective` — GraphQL forwarding

## Validation

```bash
go test -race -timeout 60s ./x/gateway/protocol/...
go vet ./x/gateway/protocol/...
```
