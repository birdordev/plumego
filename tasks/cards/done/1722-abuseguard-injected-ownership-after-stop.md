# Card 1722 — 验证注入 limiter 生命周期和后续使用

Milestone: 稳定模块质量复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: middleware
Owned Files: `middleware/abuseguard/abuse_guard_test.go`
Depends On: 无

Goal:

验证注入 limiter 生命周期和后续使用。

Scope:

构建两套 guard 并并发 Stop 其中一套，检查剩余清理和计数。

Non-goals:

本卡范围限于所列文件。维持公开签名和现有依赖，兼容契约调整先核对调用边界。

Files:

- `middleware/abuseguard/abuse_guard_test.go`

## Impact and Ownership

主模块为 `middleware`。根模块继续使用标准库。候审目标是复核可观察契约并记录证据。

## 证据

检查号为 5,28,29,39,40，状态为补证。

已看清自有和借用 Stop 约定；多个 guard 共用一个 limiter 的清理行为需交叉验证。 [middleware/abuseguard/abuse_guard.go:150](https://github.com/spcent/plumego/blob/9430574179dc6b793524992833ce27496a5fe548/middleware/abuseguard/abuse_guard.go#L150)

[复审报告](../../../docs/evidence/stable-quality-review-94305741.md)

## Acceptance Tests

- `middleware/abuseguard/abuse_guard_test.go` 中的 `TestSharedBorrowedLimiterRemainsOwnedByCaller`

注入实例仍可由另一套使用，最终由显式所有者关闭。

Tests:

通过所列模块命令运行相邻用例并核对现有约定。

Docs Sync:

对照原契约核验，若证据要求修订公开行为，先更新实施范围。

## Validation

```sh
go test -race -timeout 60s ./middleware/abuseguard
```

Done Definition:

- [x] 注入实例仍可由另一套使用，最终由显式所有者关闭。
- [x] 执行验证命令，记录状态、首次失败和剩余核验范围。

Outcome:

停止借用方后，另一 guard 可继续准入，默认时钟的后台清理仍工作。最终由创建方显式 Stop。

## 实施证据

- [TestSharedBorrowedLimiterRemainsOwnedByCaller](../../../middleware/abuseguard/abuse_guard_test.go#L416)

[批次验证与基准](../review-94305741/README.md)
