# x/ai/sse

> **Import path:** `github.com/birdordev/plumego/x/ai/sse` — sub-package of [`x/ai`](../README.md).

## Purpose

`x/ai/sse` provides Server-Sent Events (SSE) transport primitives for streaming
AI responses over HTTP: event serialization, the stream write lifecycle, and a
`net/http` handler helper.

## Status

`experimental surface` — APIs may change; parent family `x/ai` is experimental.
See [`docs/EXTENSION_MATURITY.md`](../../../../EXTENSION_MATURITY.md).

## Use this module when

- streaming AI completions to a browser or client over SSE
- writing SSE events with correct framing and flush semantics
- wiring an SSE stream into a `net/http` handler

## Do not use this module for

- workflow progress semantics — use `x/ai/streaming`
- provider adapters or session lifecycle — use `x/ai/provider`, `x/ai/session`
- generic WebSocket transport — use `x/websocket`
- app-level route registration (caller-owned)

## Public entrypoints

- `Event` — SSE event type and serialization
- `Stream` / `NewStream` — HTTP SSE stream write lifecycle
- `Handler` / `NewHandler` — `net/http` stream handler helper

## Validation

```bash
go test -race -timeout 60s ./x/ai/sse/...
go test -timeout 20s ./x/ai/sse/...
go vet ./x/ai/sse/...
```
