# Card 2116 — 报告里的手动就绪与组件健康独立

Milestone: 稳定模块第 9 节复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: x/observability/ops
Owned Files: `x/observability/ops/healthhttp/metrics_test.go`
Depends On: 2066

Goal: 报告里的手动就绪与组件健康独立
Scope: 完成健康检查后手动置为未就绪，再取报告和组件快照。
Non-goals: 范围限于本卡涉及的契约，保持公开签名和依赖。
Files: `x/observability/ops/healthhttp/metrics_test.go`。
Tests: 报告的健康保持原观察值，Readiness 保留手动原因及时间。依赖 2066。
Docs Sync: 核对新增验收与已有模块说明一致，记录证据和限制。
Done Definition: 完成以下验收，记录命令、结果与剩余限制。
Outcome: 健康检查之后手动标记未就绪，单次报告仍同时保留已观察健康、手动原因与原时间。

## 证据与影响

证据状态为补证。第 9 节检查号为 28,32,42,47。 [质量规则第 9 节](../../../docs/operations/agent-code-quality-rules.md#L267)

即使 2066 统一读取，手动就绪和检查结果仍代表不同决策。 [x/observability/ops/healthhttp/manager.go:127](../../../x/observability/ops/healthhttp/manager.go#L127)

[复审报告](../../../docs/evidence/stable-quality-review-2f962d21.md)

## 验收与验证

- [x] 报告的健康保持原观察值，Readiness 保留手动原因及时间。依赖 2066。
- [x] 已有测试覆盖相同触发和断言时可直接复用，记录具体用例。

```sh
go test -race -timeout 60s ./x/observability/ops/healthhttp
```

## 实施证据

- [TestReportPreservesIndependentReadinessAndOwnedLists](../../../x/observability/ops/healthhttp/metrics_test.go#L616)
