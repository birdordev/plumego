# x/ai/streaming

> **Import path:** `github.com/birdordev/plumego/x/ai/streaming` — sub-package of [`x/ai`](../README.md).

## Purpose

`x/ai/streaming` provides streaming coordination contracts, SSE progress update
delivery, stream registration, and streaming orchestration wrappers. It is the
stable beta surface for long-running LLM responses that must be streamed to
clients.

## Status

`beta surface` — API unchanged across v1.0.0 → v1.1.0, owner sign-off recorded.  
Parent family `x/ai` remains experimental. See [`docs/concepts/extension-maturity.md`](../../../../concepts/extension-maturity.md).

## Use this module when

- streaming LLM responses to HTTP clients as server-sent events
- tracking and surfacing workflow progress updates during a long LLM task
- coordinating multiple concurrent streams within a session

## Do not use this module for

- provider-level completion contracts — use `x/ai/provider`
- session history management — use `x/ai/session`
- tool execution policy — use `x/ai/tool`
- SSE at the middleware layer — use the `x/ai/sse` subpackage if available

## Notes

- Only use this package when streaming coordination is a concrete requirement;
  non-streaming completions via `x/ai/provider` are simpler and sufficient for
  most use cases.
- Stream registration and lifecycle ownership is explicit; callers register and
  deregister streams manually.

## Validation

```bash
go test -race -timeout 60s ./x/ai/streaming/...
go vet ./x/ai/streaming/...
```
