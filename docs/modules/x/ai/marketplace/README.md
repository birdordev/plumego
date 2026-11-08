# x/ai/marketplace

> **Import path:** `github.com/birdordev/plumego/x/ai/marketplace` — sub-package of [`x/ai`](../README.md).

## Purpose

`x/ai/marketplace` is an agent registry and installer for distributing and
loading reusable AI agents from local registries. It owns agent metadata, a
file-based registry, an installer, a validator, and a manager that orchestrates
lifecycle.

## Status

`experimental surface` — APIs may change; installation and rating APIs are not
yet stable. Parent family `x/ai` is experimental.
See [`docs/EXTENSION_MATURITY.md`](../../../../EXTENSION_MATURITY.md).

## Use this module when

- registering and loading reusable agents from a local file-based registry
- validating agent metadata and schema correctness offline
- orchestrating registry, installer, and lifecycle through a single manager

## Do not use this module for

- provider routing or session lifecycle — use `x/ai/provider`, `x/ai/session`
- workflow execution — use `x/ai/orchestration`
- remote registry hosting / cloud distribution — wire adapters at the app level

## Public entrypoints

- `AgentMetadata`, `AgentCategory` — agent value type and taxonomy
- `Registry`, `LocalRegistry` / `NewLocalRegistry` — registry interface and file-backed impl
- `Installer` / `NewInstaller` — loads and validates agents from registry entries
- `Validator` — metadata and schema validation
- `Manager` / `NewManager` — registry/installer/lifecycle orchestration

## Validation

```bash
go test -race -timeout 60s ./x/ai/marketplace/...
go test -timeout 20s ./x/ai/marketplace/...
go vet ./x/ai/marketplace/...
```
