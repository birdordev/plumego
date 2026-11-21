# Card 1698 — 合并器成功回放未扣减等待者计数

Milestone: 稳定模块质量复审
Recipe: specs/change-recipes/fix-bug.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: middleware
Owned Files: `middleware/singleflight/singleflight.go`, `middleware/singleflight/response_regression_test.go`
Depends On: 无

Goal:

合并器成功回放未扣减等待者计数。

Scope:

删除无人消费的计数，或把等待者所有权统一收束在退出 defer，确保每次只更新一次。

Non-goals:

本卡范围限于所列文件。维持公开签名和现有依赖，兼容契约调整先核对调用边界。

Files:

- `middleware/singleflight/singleflight.go`
- `middleware/singleflight/response_regression_test.go`

## Impact and Ownership

主模块为 `middleware`。根模块继续使用标准库。拟编辑范围与运行影响见本卡证据及验收。涉及安全与生命周期的变化按公开入口核验。

## 证据

检查号为 28,29,32,35，状态为源码确认。

等待者入队递增；取消、超时和失败分支部分递减，但成功回放路径结束后未扣减。当前公开 Stats 只返回活跃键，未发现外部统计错报。 [middleware/singleflight/singleflight.go:265](https://github.com/spcent/plumego/blob/9430574179dc6b793524992833ce27496a5fe548/middleware/singleflight/singleflight.go#L265)

[复审报告](../../../docs/evidence/stable-quality-review-94305741.md)

## Acceptance Tests

- `middleware/singleflight/response_regression_test.go` 中的 `TestWaiterUsesLeaderCompletionTime`
- `middleware/singleflight/response_regression_test.go` 中的 `TestWaiterWriteOutcomeControlsReplayHooks`
- `middleware/singleflight/response_regression_test.go` 中的 `TestWaiterCancellationWinsOverOtherReadyOutcomes`

覆盖成功、写失败、取消及超时，确认没有受影响的公开结果或钩子。

Tests:

通过所列模块命令运行相邻用例并核对现有约定。

Docs Sync:

对照原契约核验，若证据要求修订公开行为，先更新实施范围。

## Validation

```sh
go test -race -timeout 60s ./middleware/singleflight
```

Done Definition:

- [x] 覆盖成功、写失败、取消及超时，确认没有受影响的公开结果或钩子。
- [x] 执行验证命令，记录状态、首次失败和剩余核验范围。

Outcome:

统一由等待函数退出归还计数。成功、短写、写入失败、拒绝、超时和取消分支均通过，原有 hooks 维持原语义。

## 实施证据

- [TestWaiterUsesLeaderCompletionTime](../../../middleware/singleflight/response_regression_test.go#L142)
- [TestWaiterWriteOutcomeControlsReplayHooks](../../../middleware/singleflight/response_regression_test.go#L632)
- [TestWaiterCancellationWinsOverOtherReadyOutcomes](../../../middleware/singleflight/response_regression_test.go#L102)

[批次验证与基准](../review-94305741/README.md)
