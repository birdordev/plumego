# timeout — Request Timeout Middleware

Enforces a maximum duration for request processing.

## Import

```go
import "github.com/spcent/plumego/middleware/timeout"
```

## Overview

`timeout.Middleware` cancels the request context when the deadline is exceeded and returns `504 Gateway Timeout`. Handlers should observe `ctx.Done()` to stop work promptly.

## Exports

| Symbol | Description |
|--------|-------------|
| `Middleware(cfg Config)` | Constructor; returns a `middleware.Middleware` |
| `Config{Timeout, MaxBufferBytes, MaxReplayBytes, OnPanic}` | Timeout, buffer limits, and canceled panic hook |
| `DefaultConfig()` | 10 MiB buffer, 512 KiB replay limit; timeout disabled |

## Usage

```go
cfg := timeout.DefaultConfig()
cfg.Timeout = 10 * time.Second
app.Use(timeout.Middleware(cfg))
```

## Notes

- A non-positive `Timeout` disables the middleware. Set a positive duration explicitly.
- Responses larger than `MaxBufferBytes` or `MaxReplayBytes` become a structured `500`, including the request ID from the request context. Partial bodies and downstream header edits are discarded on errors.
- Outer response headers are cloned into the downstream buffer. Successful responses replay downstream header changes, including explicit deletion; timeout and buffer-limit errors preserve the original outer headers.
- Explicit parent cancellation returns without writing a synthetic error. An expired parent or middleware deadline returns a structured `504`. Requests already canceled before execution do not invoke the downstream handler.
- Cancellation observed at the response commitment check takes priority over a completed result or panic. Cancellation after that check does not undo response commitment.
- Timeout does not forcibly kill goroutines or wait for downstream exit. Handlers must observe `r.Context().Done()`; body writes after cancellation return the context error.
- Panics accepted before cancellation are re-thrown for outer recovery middleware. `OnPanic` observes panics discarded because of cancellation exactly once, after response handling finishes. The hook must not block; hook panics are recovered internally.
- Informational statuses such as `100`, `102`, and `103` are not forwarded by this buffering middleware and do not replace the final response status. Streaming, SSE, and connection upgrades are unsupported.

Buffered responses freeze ordinary headers at the first final WriteHeader or Write. Later ordinary header edits are ignored; declared trailers and TrailerPrefix values are replayed after the body.

Route identity can pass through timeout buffering to outer observers. Publication is synchronized and never writes headers or body, including when downstream routing finishes after cancellation. Observations already completed before a late match retain their original path.
