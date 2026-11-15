# singleflight — Request Coalescing Middleware

Deduplicates in-flight identical requests so thundering herds collapse to a single backend call.

## Import

```go
import "github.com/birdordev/plumego/middleware/singleflight"
```

## Overview

`singleflight` groups concurrent requests by their coalescing key. One leader
executes the handler, and waiters receive a replay of its captured response.
HEAD waiters receive the status and headers only.

## Exports

| Symbol | Description |
|--------|-------------|
| `New(config Config)` | Constructor returning `*Coalescer` |
| `Config.KeyFunc` | Selects the coalescing key; defaults to `DefaultKeyFunc` |
| `Config.Methods` | Methods eligible for coalescing; defaults to `GET` and `HEAD` |
| `Config.Timeout` | Maximum waiter duration; defaults to 30 seconds |
| `Config.MaxResponseBytes` | Maximum captured response body; defaults to 10 MiB |
| `Config.OnCoalesced` | Optional callback after a successful replay |
| `Config.OnError` | Optional error callback |
| `Config.AddCoalescedHeader` | Opt in to `X-Coalesced: true` on replays |
| `(*Coalescer) Middleware()` | Returns `func(http.Handler) http.Handler` |

## Usage

```go
package example

import (
	"net/http"
	"time"

	"github.com/birdordev/plumego/middleware/singleflight"
)

func coalesce(next http.Handler) http.Handler {
	cfg := singleflight.Config{
		Timeout:          2 * time.Second,
		MaxResponseBytes: 1 << 20,
	}
	return singleflight.New(cfg).Middleware()(next)
}
```

## Notes

- Use coalescing for safe, bounded responses whose variants are represented by
  the key. See the [middleware guide](../../docs/modules/middleware/README.md)
  for key selection and sensitive responses.
- A timed-out waiter receives a structured 504. A canceled waiter returns
  without a response. A failed or unreplayable leader response yields a
  structured 502 for waiters.
- If writing a replay fails after commitment, the error callback receives a
  fixed diagnostic.

Informational responses do not commit the final status. Their header snapshots are isolated from final headers. Normal header-only handlers commit implicit 200 responses consistently for leaders and waiters.
