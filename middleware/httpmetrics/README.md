# httpmetrics — HTTP Metrics Collection Middleware

Emits per-request metrics for collection by external observability systems.

## Import

```go
import "github.com/birdordev/plumego/middleware/httpmetrics"
```

## Overview

`httpmetrics.Middleware` observes request starts/completions and forwards timing, status, and cardinality data to a `metrics.Observer`. It is a thin adapter: the actual metric storage, sampling, and export belong in an observability package (e.g., `x/observability`).

## Exports

| Symbol | Description |
|--------|-------------|
| `Middleware(collector Observer)` | Constructor accepting a metrics observer |
| `Observer` | Interface for receiving request lifecycle events |

## Usage

```go
app.Use(httpmetrics.Middleware(myPrometheusObserver))
```

## Notes

- This middleware is intentionally minimal; rich labeling, histogram buckets, and cardinality guardrails live in the `Observer` implementation.
- `httpmetrics` emits timing before response flush so streaming responses are measured by handler completion, not transfer completion.
