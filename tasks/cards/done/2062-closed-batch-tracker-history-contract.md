# Card 2062 — 关闭后显式批检查的历史记录语义

Milestone: 稳定模块第 9 节复审
Recipe: specs/change-recipes/analysis-only.yaml
Context Package: review
Priority: P2
State: done
Primary Module: x/observability/ops
Owned Files: `x/observability/ops/healthhttp/history_test.go`
Depends On: 无

Goal: 关闭后显式批检查的历史记录语义
Scope: 比较关闭前、关闭后对 CheckAllComponents 的返回和历史，以及实际尝试计数。
Non-goals: 范围限于本卡涉及的契约，保持公开签名和依赖。
Files: `x/observability/ops/healthhttp/history_test.go`。
Tests: 明确历史的既有口径，维持关闭不可恢复 readiness，后续修复由验证结论决定。
Docs Sync: 核对新增验收与已有模块说明一致，记录证据和限制。
Done Definition: 完成以下验收，记录命令、结果与剩余限制。
Outcome: 仅分析。关闭后批检查增加一条调用历史，回调与尝试统计均不增长，关闭状态和未就绪持续。

## 证据与影响

证据状态为补证。第 9 节检查号为 16,28,31,35,42。 [质量规则第 9 节](../../../docs/operations/agent-code-quality-rules.md#L267)

Tracker 批次会无条件追加历史，内置 Manager 在关闭后不会执行回调。需确认记录代表调用还是实际运行。 [x/observability/ops/healthhttp/tracker.go:267](../../../x/observability/ops/healthhttp/tracker.go#L267)

[复审报告](../../../docs/evidence/stable-quality-review-c3a77410.md)

## 验收与验证

- [x] 明确历史的既有口径，维持关闭不可恢复 readiness，后续修复由验证结论决定。
- [x] 验收以记录观察结果和兼容性判断为准。

## 实施证据

执行一个正常批次后关闭，再显式执行一次。聚合历史从 1 到 2，组件回调与 CheckCount 保持 1，批次返回 unhealthy，Ready 维持 false。历史按批次调用记账，组件统计按实际尝试记账。 临时探针通过，工作区内未新增对应实现。

```sh
go test -race -count=1 -v -overlay=/tmp/c3a-impl-analysis-overlay.json ./x/observability/ops/healthhttp -run '^TestReviewClosedBatchRecordsInvocationHistory$'
```
