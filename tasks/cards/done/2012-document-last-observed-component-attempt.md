# Card 2012 — 说明 LastStatus 的时间语义

Milestone: 稳定模块第 9 节复审
Recipe: specs/change-recipes/fix-bug.yaml
Context Package: implementation
Priority: P3
State: done
Primary Module: x/observability/ops
Owned Files: `x/observability/ops/healthhttp/tracker.go`, `x/observability/ops/healthhttp/metrics_test.go`, `docs/modules/x/observability/ops/README.md`
Depends On: 无

Goal: 说明 LastStatus 的时间语义
Scope: 在字段或跟踪入口的 GoDoc 说明内部 Manager 的最近计量尝试与发布状态的关系，保留外部管理器快照回退。
Non-goals: 范围限于本卡涉及的契约，保持公开签名和依赖。
Files: `x/observability/ops/healthhttp/tracker.go`, `x/observability/ops/healthhttp/metrics_test.go`, `docs/modules/x/observability/ops/README.md`。
Tests: 用通道固定旧成功晚于新失败的顺序，断言 LastStatus、历史及已发布状态分别符合契约。
Docs Sync: 已同步[所属模块说明](../../../docs/modules/x/observability/ops/README.md)，网站生成数据核对通过。
Done Definition: 完成以下验收，记录命令、结果与剩余限制。
Outcome: 已说明。LastStatus 跟随最近计量结果。两种较旧结果被拒绝发布的交错仍按各自结果计量。

## 证据与影响

证据状态为源码确认。第 9 节检查号为 6,16,31,42。 [质量规则第 9 节](../../../docs/operations/agent-code-quality-rules.md#L267)

recordComponentCheck 按 Tracker 接收结果更新 LastStatus。Manager 的发布版本可丢弃较旧回调，故两处状态可合法分离。公开字段缺少顺序说明。 [x/observability/ops/healthhttp/tracker.go:71](../../../x/observability/ops/healthhttp/tracker.go#L71)

[复审报告](../../../docs/evidence/stable-quality-review-c3a77410.md)

## 验收与验证

- [x] 用通道固定旧成功晚于新失败的顺序，断言 LastStatus、历史及已发布状态分别符合契约。
- [x] 已有测试覆盖相同触发和断言时可直接复用，记录具体用例。

```sh
go test -race -timeout 60s ./x/observability/ops/healthhttp
```

## 实施证据

- [TestTrackerDirectMetricsUseOwnAttempt](../../../x/observability/ops/healthhttp/metrics_test.go#L431)
- [TestTrackerExternalManagerDirectSnapshotFallback](../../../x/observability/ops/healthhttp/metrics_test.go#L517)
