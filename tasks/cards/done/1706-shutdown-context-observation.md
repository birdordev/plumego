# Card 1706 — 验证排水计数正常时终止恢复行为

Milestone: 稳定模块质量复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: core
Owned Files: `core/lifecycle_test.go`
Depends On: 无

Goal:

验证排水计数正常时终止恢复行为。

Scope:

用可控 logger 屏障记录一次 tick 与取消边界。

Non-goals:

本卡范围限于所列文件。维持公开签名和现有依赖，兼容契约调整先核对调用边界。

Files:

- `core/lifecycle_test.go`

## Impact and Ownership

主模块为 `core`。根模块继续使用标准库。候审目标是复核可观察契约并记录证据。

## 证据

检查号为 26,29,32,36，状态为补证。

排水循环从上下文和 open 值判退出，诊断可能并发进入 logger。需要区分日志回调完成和 server 状态。 [core/lifecycle.go:171](https://github.com/spcent/plumego/blob/9430574179dc6b793524992833ce27496a5fe548/core/lifecycle.go#L171)

[复审报告](../../../docs/evidence/stable-quality-review-94305741.md)

## Acceptance Tests

- `core/lifecycle_test.go` 中的 `TestConnectionDrainStopsSchedulingAfterCanceledCallback`

不再调度新诊断，连接已释放且后续关停调用有效。

Tests:

通过所列模块命令运行相邻用例并核对现有约定。

Docs Sync:

对照原契约核验，若证据要求修订公开行为，先更新实施范围。

## Validation

```sh
go test -race -timeout 60s ./core
```

Done Definition:

- [x] 不再调度新诊断，连接已释放且后续关停调用有效。
- [x] 执行验证命令，记录状态、首次失败和剩余核验范围。

Outcome:

可控诊断回调退出后不再调度同一轮排水，最后连接关闭会释放活动槽，后续关停可以重新取得该槽。

## 实施证据

- [TestConnectionDrainStopsSchedulingAfterCanceledCallback](../../../core/lifecycle_test.go#L1147)

[批次验证与基准](../review-94305741/README.md)
