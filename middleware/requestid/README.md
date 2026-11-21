# requestid — Request Correlation ID Middleware

Injects a canonical request ID into each request for distributed tracing and log correlation.

## Import

```go
import "github.com/birdordev/plumego/middleware/requestid"
```

## Overview

`requestid.Middleware` selects a correlation ID from the request context first, then from a valid incoming `X-Request-ID` header, and generates one if neither supplies a valid ID. It stores the selected ID in the request context and writes it to the response header. By default it also writes that ID to the downstream request header. It copies the
header map before writing so the incoming request headers remain intact.

## Exports

| Symbol | Description |
|--------|-------------|
| `Middleware(opts ...Option)` | Constructor; variadic options |
| `WithGenerator(fn)` | Custom ID generator (default: fixed-width base62) |
| `WithRequestHeader(enabled)` | Write the selected ID to the downstream request header (default: true). With false, the next handler sees the incoming request header as received. |
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
