# recovery — Panic Recovery Middleware

Protects the application from crashing when a request handler panics.

## Import

```go
import "github.com/spcent/plumego/middleware/recovery"
```

## Overview

`recovery.Middleware` catches panics in downstream handlers, logs sanitized metadata (without leaking internal details), and returns a generic `500 Internal Server Error` to the client.

## Exports

| Symbol | Description |
|--------|-------------|
| `Middleware(config Config)` | Constructor; returns a `middleware.Middleware` |
| `Config{Logger}` | Required configuration with a `log.StructuredLogger` |
| `ErrNilLogger` | Returned when `Config.Logger` is nil |

## Usage

```go
mw, err := recovery.Middleware(recovery.Config{Logger: logger})
if err != nil {
    return err
}
app.Use(mw)
```

## Notes

- Place `recovery` early in the middleware chain (before handlers that may panic).
- Only sanitized panic metadata is logged; raw panic values are neither logged nor returned.
- Informational statuses (`100`, `102`, `103`) do not commit the final response; a later panic can still produce a structured `500`.
- `http.ErrAbortHandler` is re-thrown unchanged so `net/http` can abort the response. An ordinary panic cannot replace a final response that was already committed.
- `recovery` does not recover panics inside goroutines launched by handlers.
