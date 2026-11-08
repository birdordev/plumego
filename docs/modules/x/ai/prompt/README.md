# x/ai/prompt

> **Import path:** `github.com/birdordev/plumego/x/ai/prompt` — sub-package of [`x/ai`](../README.md).

## Purpose

`x/ai/prompt` provides prompt template management with variable substitution,
versioning, and tag-based queries. It supplies a rendering engine, a storage
interface with an in-memory implementation, and a built-in template library.

## Status

`experimental surface` — APIs may change; parent family `x/ai` is experimental.
See [`docs/EXTENSION_MATURITY.md`](../../../../EXTENSION_MATURITY.md).

## Use this module when

- managing versioned prompt templates with variable substitution
- rendering templates deterministically for AI workflows
- starting from common prompt patterns in the built-in library

## Do not use this module for

- provider adapter or session lifecycle ownership
- persistent template storage backends — wire an adapter via `Storage`
- A/B testing, experiment routing, or business-specific prompt flows

## Public entrypoints

- `Template`, `Variable` — template contract and variable definitions
- `Engine` / `NewEngine` — template rendering with variable substitution
- `Storage`, `MemoryStorage` / `NewMemoryStorage` — storage interface and in-memory impl
- `BuiltinTemplates` — built-in template library

## Validation

```bash
go test -race -timeout 60s ./x/ai/prompt/...
go test -timeout 20s ./x/ai/prompt/...
go vet ./x/ai/prompt/...
```
