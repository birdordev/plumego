# bodylimit — Request Body Size Limit Middleware

Enforces a maximum request body size.

## Import

```go
import "github.com/spcent/plumego/middleware/bodylimit"
```

## Overview

`bodylimit.Middleware` wraps request bodies with a size limit. The limit is enforced as the handler reads the body. An overrun becomes a structured `413 Payload Too Large` if the response has not already been committed.

## Exports

| Symbol | Description |
|--------|-------------|
| `Middleware(config Config)` | Constructor |
| `DefaultConfig(maxBytes, logger)` | Sensible defaults with a logger |
| `Config{MaxBytes, Logger}` | Configuration |

## Usage

```go
app.Use(bodylimit.Middleware(bodylimit.DefaultConfig(1<<20, logger))) // 1 MiB
```

## Notes

- Limits apply to the body reader, not request headers.
- The wrapped `ResponseWriter` supports `http.Flusher` and `http.Hijacker` passthrough.
- The limit is checked lazily during `Read`; a handler that never reads the body will not trigger the check.

Overruns are recorded by the reader and emitted by the response-owning goroutine
before its next write or when the downstream call returns. Reads never write
to the original response, including reads finishing after an inner timeout.
An active request receives `413` only if final headers or a flush have not
already committed a response. Later body writes are suppressed.

Place `securityheaders` outside timeout and bodylimit, and prefer
`timeout → bodylimit` so body-limit errors use timeout's response buffer.
