# x/ai/session

> **Import path:** `github.com/birdordev/plumego/x/ai/session` — sub-package of [`x/ai`](../README.md).

## Purpose

`x/ai/session` manages AI conversation session lifecycle, message storage, TTL,
token-budget-aware trimming, and active-context selection. It is the stable beta
surface for maintaining multi-turn conversation state.

## Status

`beta surface` — API unchanged across v1.0.0 → v1.1.0, owner sign-off recorded.  
Parent family `x/ai` remains experimental. See [`docs/concepts/extension-maturity.md`](../../../../concepts/extension-maturity.md).

## Use this module when

- maintaining multi-turn conversation history for an LLM session
- managing session TTL and automatic expiry
- trimming conversation history to fit within a token budget
- selecting which messages constitute the active context for a completion request

## Do not use this module for

- provider routing and completion contracts — use `x/ai/provider`
- streaming coordination — use `x/ai/streaming`
- tool execution — use `x/ai/tool`

## Notes

- Session message append is explicit; the caller owns the message log and token budget.
- Token-budget trim removes oldest messages first; the system prompt is preserved.
- Active-context selection is deterministic given the same session state and budget.

## Validation

```bash
go test -race -timeout 60s ./x/ai/session/...
go vet ./x/ai/session/...
```
