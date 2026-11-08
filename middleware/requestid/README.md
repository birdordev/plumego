# requestid — Request Correlation ID Middleware

Injects a canonical request ID into each request for distributed tracing and log correlation.

## Import

```go
import "github.com/birdordev/plumego/middleware/requestid"
```

## Overview

`requestid.Middleware` stamps every request with a unique correlation ID, writes it to the response header, and stores it in the request context. If the inbound request already carries a `X-Request-ID` header, it is preserved.

## Exports

| Symbol | Description |
|--------|-------------|
| `Middleware(opts ...Option)` | Constructor; variadic options |
| `WithGenerator(fn)` | Custom ID generator (default: fixed-width base62) |
| `WithRequestHeader(enabled)` | Write ID back to inbound request header |
| `NewRequestID()` | Default generator; returns a 12-character base62 correlation ID |
| `contract.RequestIDFromContext(ctx)` | Retrieve request ID from context |

## Usage

```go
app.Use(
    requestid.Middleware(),
)
```

Or with a custom generator:

```go
app.Use(requestid.Middleware(
    requestid.WithGenerator(mySnowflakeID),
))
```

## Notes

- Request IDs are correlation identifiers only. Do **not** use them for security, authorization, or as tokens.
- `contract.RequestIDFromContext` returns the empty string when the context has no request ID.

- `RequestIDGenerator` supports concurrent generation, including concurrent
  first use of its zero value. Initialization completes once before any random
  pool reads; callers must not copy an initialized generator.
- The default format contains a timestamp component. It is a correlation ID,
  not a cryptographic secret or a UUID.
