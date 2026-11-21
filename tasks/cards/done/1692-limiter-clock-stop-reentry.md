# Card 1692 — 清理时钟回调重入 Stop 导致互等

Milestone: 稳定模块质量复审
Recipe: specs/change-recipes/fix-bug.yaml
Context Package: implementation
Priority: P1
State: done
Primary Module: security
Owned Files: `security/abuse/limiter.go`, `security/abuse/limiter_test.go`, `docs/modules/security/README.md`
Depends On: 无

Goal:

清理时钟回调重入 Stop 导致互等。

Scope:

明确时钟回调必须可快速返回并与关停分离。若需要支持轻量可重入时钟，后台使用 ticker 时间，把可注入时钟的业务用途和兼容要求先写清楚。

Non-goals:

本卡范围限于所列文件。维持公开签名和现有依赖，兼容契约调整先核对调用边界。

Files:

- `security/abuse/limiter.go`
- `security/abuse/limiter_test.go`
- `docs/modules/security/README.md`

## Impact and Ownership

主模块为 `security`。根模块继续使用标准库。拟编辑范围与运行影响见本卡证据及验收。涉及安全与生命周期的变化按公开入口核验。

## 证据

检查号为 28,29,33,40,47，状态为已复现。

注入的 Config.Now 由清理协程调用。回调同步调用 Stop 后等待同一协程的退出信号，隔离测试定位到两者互等。 [security/abuse/limiter.go:462](https://github.com/spcent/plumego/blob/9430574179dc6b793524992833ce27496a5fe548/security/abuse/limiter.go#L462)

[复审报告](../../../docs/evidence/stable-quality-review-94305741.md)

## Acceptance Tests

- `security/abuse/limiter_test.go` 中的 `TestAcceptanceCustomClockCleanupAndStop`
- `security/abuse/limiter_test.go` 中的 `TestConcurrentStopWaitsForCleanupAndKeepsAdmissionUsable`
- `middleware/abuseguard/abuse_guard_test.go` 中的 `TestSharedBorrowedLimiterRemainsOwnedByCaller`

用独立进程或合成时间验证最终清理和 Stop 的时钟约定，多调用者仍同步等待实际清理退出。

Tests:

通过所列模块命令运行相邻用例并核对现有约定。

Docs Sync:

复核 `docs/modules/security/README.md`，模块说明改动后核对生成文件差异。

## Validation

```sh
go test -race -timeout 60s ./security/abuse
make website-sync
make website-check-generated
```

Done Definition:

- [x] 用独立进程或合成时间验证最终清理和 Stop 的时钟约定，多调用者仍同步等待实际清理退出。
- [x] 执行验证命令，记录状态、首次失败和剩余核验范围。

Outcome:

默认时钟仍定时清理。注入时钟在 Allow 中采样并触发受间隔约束的清理，保持同一时间域。Stop 禁止新扫描，等待在途扫描，回调可以调用 Stop，准入仍可用。

## 实施证据

- [TestAcceptanceCustomClockCleanupAndStop](../../../security/abuse/limiter_test.go#L868)
- [TestConcurrentStopWaitsForCleanupAndKeepsAdmissionUsable](../../../security/abuse/limiter_test.go#L844)
- [TestSharedBorrowedLimiterRemainsOwnedByCaller](../../../middleware/abuseguard/abuse_guard_test.go#L416)

[批次验证与基准](../review-94305741/README.md)
