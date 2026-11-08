# x/ai/tool

> **Import path:** `github.com/birdordev/plumego/x/ai/tool` — sub-package of [`x/ai`](../README.md).

## Purpose

`x/ai/tool` owns AI tool execution contracts, an explicit tool registry,
tool execution policy interfaces, and local builtin tools. It is the stable
beta surface for defining and invoking tools that LLMs can call during a
completion.

## Status

`beta surface` — API unchanged across v1.0.0 → v1.1.0, owner sign-off recorded.  
Parent family `x/ai` remains experimental. See [`docs/concepts/extension-maturity.md`](../../../../concepts/extension-maturity.md).

## Use this module when

- defining named tools that an LLM can invoke (function calling)
- building an explicit tool registry that routes LLM tool calls to Go functions
- enforcing tool execution policy (allow-list, deny-list, execution budget)
- adding local builtin tools (file read, web fetch, calculator, etc.)

## Do not use this module for

- provider routing — use `x/ai/provider`
- session management — use `x/ai/session`
- orchestration loops that chain multiple tool calls — use `x/ai/orchestration` (experimental)

## Notes

- All tools are registered explicitly; there is no reflection-based auto-discovery.
- Tool execution results must conform to the `tool.Result` contract; handlers
  return typed results, not raw JSON strings.
- Policy interfaces allow callers to inject allow/deny logic without modifying
  the registry.

## Validation

```bash
go test -race -timeout 60s ./x/ai/tool/...
go vet ./x/ai/tool/...
```
