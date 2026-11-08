# abuseguard — Abuse Detection & Rate Limit Middleware

Rate-limits and blocks abusive request patterns.

## Import

```go
import "github.com/birdordev/plumego/middleware/abuseguard"
```

## Overview

`abuseguard` is a composite middleware that drops, delays, or throttles requests deemed abusive by an underlying abuse decision engine. It is transparent to well-behaved clients and applies `Retry-After` / `X-RateLimit-*` headers to rejected requests.

## Exports

| Symbol | Description |
|--------|-------------|
| `NewAbuseGuard(config)` | Constructor returning `*AbuseGuardMiddleware` |
| `AbuseGuardConfig{MaxRequests, Window, Burst, ...}` | Configuration for rate window and thresholds |
| `Middleware()` | Returns the `middleware.Middleware` adapter |
| `Stop()` | Graceful shutdown of background goroutines |

## Usage

```go
guard := abuseguard.NewAbuseGuard(abuseguard.DefaultAbuseGuardConfig())
defer guard.Stop()
app.Use(guard.Middleware())
```

## Notes

- The engine evaluates every request independently; consider placing `abuseguard` early in the chain, after `recovery` but before expensive handlers.
- Configuration is validated and normalized at construction time; invalid configs return zero-value guards that reject everything safely.
