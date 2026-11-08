# x/ai/tokenizer

> **Import path:** `github.com/birdordev/plumego/x/ai/tokenizer` — sub-package of [`x/ai`](../README.md).

## Purpose

`x/ai/tokenizer` provides token-counting primitives for AI models — a
`Tokenizer` interface, approximate and model-specific implementations, a live
`StreamCounter`, and a shared `TokenUsage` value type used across the `x/ai`
family.

## Status

`experimental surface` — APIs may change; parent family `x/ai` is experimental.
See [`docs/EXTENSION_MATURITY.md`](../../../../EXTENSION_MATURITY.md).

## Use this module when

- counting tokens for prompts or completions before/after a request
- tracking token usage live during a streamed response
- sharing a `TokenUsage` value across provider, session, or cache code

## Do not use this module for

- model configuration or credential ownership
- provider adapter implementation — use `x/ai/provider`
- session or context lifecycle — use `x/ai/session`
- business-specific prompt policy

## Public entrypoints

- `Tokenizer` — token-counting interface
- `Message` — message value type for counting
- `TokenUsage` — shared usage value type
- `StreamCounter` — live token tracking for streamed output
- `SimpleTokenizer` — approximate tokenizer
- `ClaudeTokenizer` / `GPTTokenizer` — model-specific tokenizers

## Validation

```bash
go test -race -timeout 60s ./x/ai/tokenizer/...
go test -timeout 20s ./x/ai/tokenizer/...
go vet ./x/ai/tokenizer/...
```
