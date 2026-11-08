# x/ai/semanticcache/embedding

> **Import path:** `github.com/birdordev/plumego/x/ai/semanticcache/embedding` — sub-package of [`x/ai/semanticcache`](../README.md).

## Purpose

`x/ai/semanticcache/embedding` defines the embedding `Provider` interface and
HTTP-backed implementations (OpenAI, Cohere) for vectorizing text used by the
semantic cache. Adapters use `net/http` directly and are offline-testable.

## Status

`experimental surface` — APIs may change; parent family `x/ai` is experimental.
See [`docs/EXTENSION_MATURITY.md`](../../../../../EXTENSION_MATURITY.md).

## Use this module when

- generating single or batch embeddings for semantic-cache text
- wiring an OpenAI or Cohere embedding backend without a third-party SDK
- tracking embedding usage via `ProviderWithStats`

## Do not use this module for

- vector similarity search — use [`x/ai/semanticcache/vectorstore`](../vectorstore/README.md)
- cache lifecycle management — use [`x/ai/semanticcache/cachemanager`](../cachemanager/README.md)
- LLM completion provider adapters — use `x/ai/provider`
- persistent embedding storage

## Public entrypoints

- `Provider` — embedding generation interface (single and batch)
- `Embedding` — vector value type with metadata and token cost
- `Config`, `Stats`, `BatchRequest`, `BatchResponse` — supporting types
- `ProviderWithStats` / `NewProviderWithStats` — usage-tracking wrapper
- `OpenAIProvider`, `OpenAIConfig` / `NewOpenAIProvider` — OpenAI adapter
- `CohereProvider`, `CohereConfig` / `NewCohereProvider` — Cohere adapter

## Validation

```bash
go test -race -timeout 60s ./x/ai/semanticcache/embedding/...
go test -timeout 20s ./x/ai/semanticcache/embedding/...
go vet ./x/ai/semanticcache/embedding/...
```
