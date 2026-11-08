# x/rpc/server

> **Import path:** `github.com/birdordev/plumego/x/rpc/server` — sub-package of [`x/rpc`](../README.md).

## Purpose

`x/rpc/server` is a caller-owned RPC server lifecycle wrapper: it wraps an RPC
runtime, delegates service registration, blocks while serving, and supports
context-aware graceful shutdown.

## Status

`experimental surface` — APIs may change; parent family `x/rpc` is experimental.
See [`docs/EXTENSION_MATURITY.md`](../../../../EXTENSION_MATURITY.md).

## Use this module when

- wrapping an RPC runtime lifecycle in a service
- registering caller-owned services explicitly
- running a blocking serve loop with graceful shutdown

## Do not use this module for

- automatic service-discovery registration
- core app bootstrap ownership
- business-specific RPC services
- HTTP route ownership — use `x/rpc/gateway`

## Public entrypoints

- `Server` / `New` — server lifecycle wrapper and constructor
- `Runtime` — RPC runtime interface
- `ErrRuntimeNil` — sentinel error for a nil runtime
- `(*Server).RegisterService` — delegate service registration
- `(*Server).Serve` — blocking serve lifecycle
- `(*Server).GracefulStop` — context-aware graceful shutdown

## Validation

```bash
go test -race -timeout 60s ./...
go test -timeout 20s ./...
go vet ./...
```
