# x/rpc/gateway

> **Import path:** `github.com/birdordev/plumego/x/rpc/gateway` — sub-package of [`x/rpc`](../README.md).

## Purpose

`x/rpc/gateway` is a dependency-free HTTP route adapter for caller-owned RPC
handlers: it builds an HTTP mux, registers path handlers, and exposes the mux as
a `net/http` handler while keeping the target gRPC address explicit.

## Status

`experimental surface` — APIs may change; parent family `x/rpc` is experimental.
See [`docs/EXTENSION_MATURITY.md`](../../../../EXTENSION_MATURITY.md).

## Use this module when

- mapping HTTP paths to caller-owned RPC handlers
- exposing RPC handlers through a `net/http` mux
- keeping the target gRPC address explicit in the adapter

## Do not use this module for

- protobuf code generation
- service discovery
- edge gateway proxy ownership — use `x/gateway`
- core app bootstrap ownership

## Public entrypoints

- `HTTPTranscoder` / `New` — HTTP-to-RPC transcoder and constructor
- `HandlerFunc` — RPC handler function type
- `Option` — transcoder option
- `(*HTTPTranscoder).Register` — register a path handler
- `(*HTTPTranscoder).Handler` — expose the mux as `net/http` handler
- `(*HTTPTranscoder).Target` — read the configured gRPC target

## Validation

```bash
go test -race -timeout 60s ./...
go test -timeout 20s ./...
go vet ./...
```
