# Card 2115 — 健康批次进入提交前取消仍按类别返回

Milestone: 稳定模块第 9 节复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: x/observability/ops
Owned Files: `x/observability/ops/healthhttp/aggregate_regression_test.go`
Depends On: 2064

Goal: 健康批次进入提交前取消仍按类别返回
Scope: 固定回调完成时间，让取消、较新批次和关闭分别先过决定点。
Non-goals: 范围限于本卡涉及的契约，保持公开签名和依赖。
Files: `x/observability/ops/healthhttp/aggregate_regression_test.go`。
Tests: 持续提供 context.Canceled 或 DeadlineExceeded 的既有状态，旧批次不覆盖新终态。依赖 2064。
Docs Sync: 核对新增验收与已有模块说明一致，记录证据和限制。
Done Definition: 完成以下验收，记录命令、结果与剩余限制。
Outcome: 用受控上下文让取消在首次读取后生效。重新取样时关闭、手动决策和更新批次仍拥有优先级，已有测试保留 DeadlineExceeded 分类。

## 证据与影响

证据状态为补证。第 9 节检查号为 26,31,33,42,47。 [质量规则第 9 节](../../../docs/operations/agent-code-quality-rules.md#L267)

2064 移动上下文方法后，取消信号与下一批版本到达的竞争需要独立验收。 [x/observability/ops/healthhttp/manager.go:344](../../../x/observability/ops/healthhttp/manager.go#L344)

[复审报告](../../../docs/evidence/stable-quality-review-2f962d21.md)

## 验收与验证

- [x] 持续提供 context.Canceled 或 DeadlineExceeded 的既有状态，旧批次不覆盖新终态。依赖 2064。
- [x] 已有测试覆盖相同触发和断言时可直接复用，记录具体用例。

```sh
go test -race -timeout 60s ./x/observability/ops/healthhttp
```

## 实施证据

- [TestBatchPublicationRevalidatesAfterRefreshingCancellation](../../../x/observability/ops/healthhttp/aggregate_regression_test.go#L378)
- [TestBatchReadinessUsesCancellationReasonWithoutNewSample](../../../x/observability/ops/healthhttp/aggregate_regression_test.go#L229)
