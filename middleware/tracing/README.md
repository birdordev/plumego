# tracing — Distributed Tracing Middleware

Propagates trace/span context through HTTP requests.

## Import

```go
import "github.com/birdordev/plumego/middleware/tracing"
```

## Overview

`tracing.Middleware` starts a span through an injected `Tracer`, attaches its trace context to the request, and ends the span when the downstream handler returns or panics. Concrete propagation and trace transport belong to the tracer implementation.

## Exports

| Symbol | Description |
|--------|-------------|
| `Middleware(tracer Tracer)` | Constructor |
| `Tracer` | Interface: `Start(context.Context, *http.Request) (context.Context, TraceSpan)` |
| `TraceSpan` | Minimal span handle abstraction |

## Usage

```go
app.Use(tracing.Middleware(myOTelTracer))
```

## Notes

- This is a thin transport adapter. Trace sampling, export, and backend wiring belong in `x/observability`.
- Standard propagation headers (`traceparent`, `x-b3-*`) are read/written by the `Tracer` implementation, not this middleware.
- Place tracing early in the middleware chain to capture request latency from the first byte.

Tracer start and span identity panics do not prevent downstream execution.
Identity failures preserve the original request context, and a span already
returned is still ended once by the best-effort finalizer. A nil returned
context falls back to the original request context. Finalizer panics do not
replace a downstream panic.
