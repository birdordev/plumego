# Card 2069 — 说明关闭后的批次历史与尝试计量

Milestone: 稳定模块第 9 节复审
Recipe: specs/change-recipes/fix-bug.yaml
Context Package: implementation
Priority: P3
State: done
Primary Module: x/observability/ops
Owned Files: `x/observability/ops/healthhttp/tracker.go`, `x/observability/ops/healthhttp/history_test.go`, `docs/modules/x/observability/ops/README.md`
Depends On: 无

Goal: 说明关闭后的批次历史与尝试计量
Scope: 在入口 GoDoc 明确 Tracker 记录批调用终态和实际组件尝试，两者有独立计量时点。
Non-goals: 范围限于本卡涉及的契约，保持公开签名和依赖。
Files: `x/observability/ops/healthhttp/tracker.go`, `x/observability/ops/healthhttp/history_test.go`, `docs/modules/x/observability/ops/README.md`。
Tests: 先完成一批再关闭并发起第二批，核对历史、组件调用数、LastCheckTime 与 readiness，保留现有语义。
Docs Sync: 已同步[所属模块说明](../../../docs/modules/x/observability/ops/README.md)，网站生成数据核对通过。
Done Definition: 完成以下验收，记录命令、结果与剩余限制。
Outcome: 完成。明确已配置管理器的批次调用历史与组件尝试统计分别计量。关闭后调用只增加一条批次历史。

## 证据与影响

证据状态为源码确认。第 9 节检查号为 16,28,31,42,52。 [质量规则第 9 节](../../../docs/operations/agent-code-quality-rules.md#L267)

上轮受控探针确认关闭后再调用批检查增加一条不可用历史，回调和计数保持原值。公开注释只描述执行检查，缺少零次尝试的历史口径。 [x/observability/ops/healthhttp/tracker.go:253](../../../x/observability/ops/healthhttp/tracker.go#L253)

[复审报告](../../../docs/evidence/stable-quality-review-2f962d21.md)

## 验收与验证

- [x] 先完成一批再关闭并发起第二批，核对历史、组件调用数、LastCheckTime 与 readiness，保留现有语义。
- [x] 已有测试覆盖相同触发和断言时可直接复用，记录具体用例。

```sh
go test -race -timeout 60s ./x/observability/ops/healthhttp
```

## 实施证据

- [TestClosedTrackerRecordsBatchResultWithoutAnotherAttempt](../../../x/observability/ops/healthhttp/history_test.go#L527)
