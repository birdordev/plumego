# Card 2118 — 报告组件名切片的调用方修改隔离

Milestone: 稳定模块第 9 节复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P3
State: done
Primary Module: x/observability/ops
Owned Files: `x/observability/ops/healthhttp/metrics_test.go`
Depends On: 2066

Goal: 报告组件名切片的调用方修改隔离
Scope: 分别改写两份组件名及依赖值，再生成报告。
Non-goals: 范围限于本卡涉及的契约，保持公开签名和依赖。
Files: `x/observability/ops/healthhttp/metrics_test.go`。
Tests: 新报告维持已注册组件，独立用途的字段不因调用方写入互相污染。依赖 2066。
Docs Sync: 核对新增验收与已有模块说明一致，记录证据和限制。
Done Definition: 完成以下验收，记录命令、结果与剩余限制。
Outcome: 改写报告的组件名不影响同份报告的依赖切片。两者对后续报告都没有影响，关闭形状保持。

## 证据与影响

证据状态为补证。第 9 节检查号为 34,42,45。 [质量规则第 9 节](../../../docs/operations/agent-code-quality-rules.md#L267)

报告返回有序的组件切片和健康依赖切片。2066 共用内部读取时需检查对外所有权。 [x/observability/ops/healthhttp/tracker.go:548](../../../x/observability/ops/healthhttp/tracker.go#L548)

[复审报告](../../../docs/evidence/stable-quality-review-2f962d21.md)

## 验收与验证

- [x] 新报告维持已注册组件，独立用途的字段不因调用方写入互相污染。依赖 2066。
- [x] 已有测试覆盖相同触发和断言时可直接复用，记录具体用例。

```sh
go test -race -timeout 60s ./x/observability/ops/healthhttp
```

## 实施证据

- [TestReportPreservesIndependentReadinessAndOwnedLists](../../../x/observability/ops/healthhttp/metrics_test.go#L616)
