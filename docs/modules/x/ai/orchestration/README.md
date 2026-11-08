# x/ai/orchestration

> **Import path:** `github.com/birdordev/plumego/x/ai/orchestration` — sub-package of [`x/ai`](../README.md).

## Purpose

`x/ai/orchestration` is a single-process agent workflow engine with sequential,
parallel, conditional, and retry step types. It owns agent definition, workflow
registration and state, and the `Engine` lifecycle.

## Status

`experimental surface` — APIs may change; parent family `x/ai` is experimental.
See [`docs/EXTENSION_MATURITY.md`](../../../../EXTENSION_MATURITY.md).

## Use this module when

- composing single-process agent workflows from reusable step types
- registering and executing workflows with shared workflow state
- wrapping any step with retry semantics via `RetryStep`

## Do not use this module for

- distributed or remote execution — use `x/ai/distributed`
- tool execution policy — use `x/ai/tool`
- agent registry — use `x/ai/marketplace`
- session lifecycle / streaming coordination — use `x/ai/session`, `x/ai/streaming`

## Public entrypoints

- `Agent`, `AgentResult` — agent definition and execution result
- `Workflow` / `NewWorkflow` — workflow registration and state
- `Step` — step interface contract
- `SequentialStep`, `ParallelStep`, `ConditionalStep`, `RetryStep` — step implementations
- `Engine` / `NewEngine` — workflow engine lifecycle (RegisterWorkflow, Execute)

## Validation

```bash
go test -race -timeout 60s ./x/ai/orchestration/...
go test -timeout 20s ./x/ai/orchestration/...
go vet ./x/ai/orchestration/...
```
