# x/ai/distributed

> **Import path:** `github.com/birdordev/plumego/x/ai/distributed` — sub-package of [`x/ai`](../README.md).

## Purpose

`x/ai/distributed` wraps `x/ai/orchestration.Engine` with durable execution:
persistent task queuing, workflow checkpointing, recovery of interrupted runs,
and remote step dispatch. Use it when single-process orchestration is not
enough.

## Status

`experimental surface` — APIs may change; parent family `x/ai` is experimental.
See [`docs/EXTENSION_MATURITY.md`](../../../../EXTENSION_MATURITY.md).

## Use this module when

- you need durable workflow execution that survives process restarts
- you want to queue and checkpoint orchestration steps via caller-provided backends
- you need to dispatch workflow steps to remote workers

## Do not use this module for

- single-process orchestration primitives — use `x/ai/orchestration`
- provider adapters or session lifecycle — use `x/ai/provider`, `x/ai/session`
- agent registry / marketplace — use `x/ai/marketplace`
- built-in queue backend implementations — supply your own via `TaskQueue`

## Public entrypoints

- `DistributedEngine` — durable engine wrapping `orchestration.Engine`
- `EngineConfig` / `DefaultEngineConfig` — engine configuration
- `TaskQueue` — caller-provided queue interface
- `WorkflowPersistence` — caller-provided checkpoint storage interface
- `NewDistributedEngine` — constructor

## Validation

```bash
go test -race -timeout 60s ./x/ai/distributed/...
go test -timeout 20s ./x/ai/distributed/...
go vet ./x/ai/distributed/...
```
