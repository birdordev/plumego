# x/rpc/client

> **Import path:** `github.com/birdordev/plumego/x/rpc/client` — sub-package of [`x/rpc`](../README.md).

## Purpose

`x/rpc/client` provides a transport-neutral RPC client connection pool and unary
client interceptors (logging, tracing, retry) for `x/rpc` callers.

## Status

`experimental surface` — APIs may change; parent family `x/rpc` is experimental.
See [`docs/EXTENSION_MATURITY.md`](../../../../EXTENSION_MATURITY.md).

## Use this module when

- pooling RPC client connections keyed by target
- managing explicit connection close lifecycle
- adding unary client logging, tracing, or retry interceptors

## Do not use this module for

- load balancing (not owned here)
- streaming interceptors
- circuit-breaker behavior — use `x/resilience`
- service discovery

## Public entrypoints

- `Pool` / `New`, `Conn`, `Dialer`, `DialOption` — connection pool and dialing
- `CallOption`, `UnaryInvoker`, `UnaryInterceptor` — unary call types
- `Code`, `CodedError` — transport status codes and errors
- `LoggingInterceptor` — unary logging interceptor
- `RetryInterceptor` — unary retry interceptor
- `TracingInterceptor`, `TraceStarter`, `TraceMetadata` — unary trace helpers

## Validation

```bash
go test -race -timeout 60s ./...
go test -timeout 20s ./...
go vet ./...
```
