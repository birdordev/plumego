# Card 1702 — 核对注入 typed nil logger 的构造约定

Milestone: 稳定模块质量复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: core
Owned Files: `core/dependencies_test.go`
Depends On: 无

Goal:

核对注入 typed nil logger 的构造约定。

Scope:

用合成 logger 对照 nil 接口和 typed nil 的文档预期。

Non-goals:

本卡范围限于所列文件。维持公开签名和现有依赖，兼容契约调整先核对调用边界。

Files:

- `core/dependencies_test.go`

## Impact and Ownership

主模块为 `core`。根模块继续使用标准库。候审目标是复核可观察契约并记录证据。

## 证据

检查号为 5,14,15,36，状态为补证。

构造对 nil 接口使用 discard，但 typed nil 指针保留。部分 logger 可能支持零值，不能仅凭接口形态推断。 [core/dependencies.go:14](https://github.com/spcent/plumego/blob/9430574179dc6b793524992833ce27496a5fe548/core/dependencies.go#L14)

[复审报告](../../../docs/evidence/stable-quality-review-94305741.md)

## Acceptance Tests

- `core/dependencies_test.go` 中的 `TestConfiguredTypedNilLoggerRemainsCallerOwned`
- `core/dependencies_test.go` 中的 `TestNewFallsBackToDiscardLogger`

明确构造、Logger 和首条生命周期日志，各自行为可重复。

Tests:

通过所列模块命令运行相邻用例并核对现有约定。

Docs Sync:

对照原契约核验，若证据要求修订公开行为，先更新实施范围。

## Validation

```sh
go test -race -timeout 60s ./core
```

Done Definition:

- [x] 明确构造、Logger 和首条生命周期日志，各自行为可重复。
- [x] 执行验证命令，记录状态、首次失败和剩余核验范围。

Outcome:

保留注入接口的动态值。空接口正常降级到 discard，可自行处理 nil receiver 的 logger 仍由调用者持有，Prepare 使用其诊断方法。

## 实施证据

- [TestConfiguredTypedNilLoggerRemainsCallerOwned](../../../core/dependencies_test.go#L57)
- [TestNewFallsBackToDiscardLogger](../../../core/dependencies_test.go#L24)

[批次验证与基准](../review-94305741/README.md)
