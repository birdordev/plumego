# x/ai/filter

> **Import path:** `github.com/birdordev/plumego/x/ai/filter` — sub-package of [`x/ai`](../README.md).

## Purpose

`x/ai/filter` provides a composable content-filtering chain for AI inputs and
outputs, including PII detection, secret scrubbing, prompt-injection guards, and
profanity filtering. Filter rules are stdlib-only and caller-composed.

## Status

`experimental surface` — APIs may change; parent family `x/ai` is experimental.
See [`docs/EXTENSION_MATURITY.md`](../../../../EXTENSION_MATURITY.md).

## Use this module when

- scrubbing PII or secrets from AI prompts and completions
- guarding against prompt-injection in untrusted input
- composing a multi-stage content-safety chain

## Do not use this module for

- provider or session lifecycle ownership — use `x/ai/provider`, `x/ai/session`
- business-specific allow/block-list policy — keep that caller-owned
- HTTP middleware integration or persistent audit logging

## Public entrypoints

- `Filter` — filter interface
- `Result` — filter result contract
- `Stage`, `Policy` — chain composition primitives
- `Chain` / `NewChain` — composable filter chain
- `PIIFilter`, `SecretFilter`, `PromptInjectionFilter`, `ProfanityFilter` — built-in filters

## Validation

```bash
go test -race -timeout 60s ./x/ai/filter/...
go test -timeout 20s ./x/ai/filter/...
go vet ./x/ai/filter/...
```
