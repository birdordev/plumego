# concurrencylimit — Concurrent Request Limit Middleware

Limits the number of concurrently processed requests.

## Import

```go
import "github.com/spcent/plumego/middleware/concurrencylimit"
```

## Overview

`concurrencylimit.Middleware` rejects requests with `503 Service Unavailable` when the active request count reaches the configured maximum. It protects the service from overload during traffic spikes.

## Exports

| Symbol | Description |
|--------|-------------|
| `Middleware(config Config)` | Constructor |
| `DefaultConfig(maxConcurrent)` | Creates config with the given limit |
| `Config{MaxConcurrent, QueueDepth, QueueTimeout}` | Active, queued, and wait limits |

## Usage

```go
app.Use(concurrencylimit.Middleware(concurrencylimit.DefaultConfig(100)))
```

## Notes

- Queue and worker slots are released when the downstream handler returns or panics.
- Canceled requests return without invoking downstream or writing a synthetic error. Cancellation is checked before admission, after acquiring a worker slot, and before writing rejection responses.
- `QueueDepth` bounds additional waiters; `QueueTimeout` bounds their wait before a structured `503` is returned.
- Place this middleware before expensive work (after `recovery`, before business handlers).
