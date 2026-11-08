# singleflight — Request Coalescing Middleware

Deduplicates in-flight identical requests so thundering herds collapse to a single backend call.

## Import

```go
import "github.com/spcent/plumego/middleware/singleflight"
```

## Overview

`singleflight` groups concurrent requests that share a cache key and lets exactly one execute. The other waiters receive the same response bytes once the leader completes, reducing load on expensive or cache-miss-prone handlers.

## Exports

| Symbol | Description |
|--------|-------------|
| `New(config Config)` | Constructor returning `*Coalescer` |
| `Config{MaxRetries, RetryDelay, KeyFunc, Window, AllowedMethods}` | Coalescing rules |
| `(*Coalescer) Middleware()` | Returns `func(http.Handler) http.Handler` |

## Usage

```go
cfg := singleflight.Config{
    KeyFunc: func(r *http.Request) string { return r.URL.Path + "-" + r.Method },
}
app.Use(singleflight.New(cfg).Middleware())
```

## Notes

- Only safe HTTP methods (`GET`, `HEAD`, `OPTIONS`) are coalesced by default.
- The middleware retries on transient errors up to `MaxRetries` before failing waiters.
- Response bytes are captured and replayed synchronously to each waiter; consider this for idempotent, cacheable endpoints only.

Informational responses do not commit the final status. Their header snapshots are isolated from final headers. Normal header-only handlers commit implicit 200 responses consistently for leaders and waiters.
