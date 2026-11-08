# accesslog — HTTP Access Log Middleware

Records structured access logs for every HTTP request.

## Import

```go
import "github.com/birdordev/plumego/middleware/accesslog"
```

## Overview

`accesslog.Middleware` captures request metadata (method, path, status code, duration, bytes sent, client IP, user agent) and emits a structured log entry via a `log.StructuredLogger`.

## Exports

| Symbol | Description |
|--------|-------------|
| `Middleware(config Config)` | Constructor |
| `Config{Logger}` | Configuration; `Logger` is required |
| `ErrNilLogger` | Returned when `Logger` is nil |

## Usage

```go
mw, err := accesslog.Middleware(accesslog.Config{Logger: logger})
if err != nil {
    return err
}
app.Use(mw)
```

## Notes

- The middleware uses `internaltelemetry` for efficient response tracking without allocating per-request wrappers in the hot path.
- Access logs are written in the log format determined by the provided `StructuredLogger` (text or JSON).
