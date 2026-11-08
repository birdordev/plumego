# x/ai/provider

> **Import path:** `github.com/birdordev/plumego/x/ai/provider` — sub-package of [`x/ai`](../README.md).

## Purpose

`x/ai/provider` owns provider-neutral completion request/response contracts,
provider routing, and concrete adapters for OpenAI and Claude. It is the stable
beta surface for connecting a Plumego service to an LLM backend.

## Status

`beta surface` — API unchanged across v1.0.0 → v1.1.0, owner sign-off recorded.  
Parent family `x/ai` remains experimental. See [`docs/concepts/extension-maturity.md`](../../../../concepts/extension-maturity.md).

## Use this module when

- connecting a service to an LLM provider (OpenAI, Claude)
- writing provider-neutral completion logic that can switch backends
- writing offline tests using the mock provider helper

## Do not use this module for

- session lifecycle management — use `x/ai/session`
- streaming coordination — use `x/ai/streaming`
- tool registry and execution policy — use `x/ai/tool`
- production import of `x/ai` root (start with the stable-tier subpackages)

## Providers

| Provider | Constructor |
|---|---|
| OpenAI | `provider.NewOpenAIProvider(opts...)` |
| Claude (Anthropic) | `provider.NewClaudeProvider(opts...)` |
| Mock (offline tests) | `provider.NewMockProvider(responses...)` |

## Notes

- All providers are tested offline via `httptest.NewServer`; no live API keys
  are needed in tests.
- Provider adapters accept functional option overrides for base URL, timeout,
  and default model; constructor defaults are safe for production use.

## Validation

```bash
go test -race -timeout 60s ./x/ai/provider/...
go vet ./x/ai/provider/...
```
