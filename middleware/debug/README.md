# debug — Debug Response Capture Middleware

Captures handler responses for debugging and testing.

## Import

```go
import "github.com/birdordev/plumego/middleware/debug"
```

## Overview

`debug.Middleware` buffers response headers and body for inspection. It is intended for development, integration tests, and diagnostic endpoints where you need to observe what a handler chain produces without mutating the real response.

## Exports

| Symbol | Description |
|--------|-------------|
| `Middleware(config Config)` | Constructor |
| `DefaultConfig()` | Sensible defaults |
| `Config{MaxBodyBytes, IncludeBody}` | Capture limit and optional body size metadata |

## Usage

```go
app.Use(debug.Middleware(debug.DefaultConfig()))
```

## Notes

- Not for production hot paths; the full response is buffered in memory.
- The wrapped `ResponseWriter` supports `Unwrap()`, `Flush()`, and `Hijack()`.
- If an outer recovery middleware completes a response already streamed by
  Debug, Debug forwards its declared trailers during unwinding. The application
  owns the connection and trailers after a successful hijack.
