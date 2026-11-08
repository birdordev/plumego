# x/observability/tracer

> **Import path:** `github.com/birdordev/plumego/x/observability/tracer` — sub-package of [`x/observability`](../README.md).

## Purpose

`x/observability/tracer` provides OpenTelemetry-style tracer construction,
sampling, span helpers, and a trace collector for wiring distributed tracing into
the app lifecycle.

## Status

`beta surface` — production-ready with caveats; parent family `x/observability`
is beta. See [`docs/EXTENSION_MATURITY.md`](../../../../EXTENSION_MATURITY.md).

## Use this module when

- constructing and configuring a tracer with sampling
- starting and annotating spans with attributes, kind, and status
- collecting traces in-process via a trace collector

## Do not use this module for

- transport-layer tracing — use `middleware/tracing`
- metrics collection — use the `metrics` stable root
- business observability policy

## Public entrypoints

- `NewTracer` / `DefaultTracerConfig` — tracer construction and defaults
- `Tracer`, `Trace`, `Span`, `SpanEvent`, `SpanLink` — tracing types
- `TraceID` / `SpanID`, `IDGenerator`, `NewRandomIDGenerator`, `RandomIDGenerator` — ID generation
- `Sampler`, `ProbabilitySampler`, `NewProbabilitySampler` — sampling
- `TraceCollector`, `SimpleTraceCollector`, `NewSimpleTraceCollector`, `TraceFilter` — collection
- `SpanOption`, `EventOption`, `ErrorOption` with `WithSpanAttributes`,
  `WithSpanKind`, `WithSpanStatus`, `WithEventAttributes`, `WithErrorAttributes`,
  `WithTraceAttributes` — span/event/trace options

## Validation

```bash
go test -race -timeout 60s ./x/observability/tracer/...
go vet ./x/observability/tracer/...
```
