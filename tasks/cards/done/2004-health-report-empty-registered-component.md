# Card 2004 — 核对报告中注册未尝试与无组件状态

Milestone: 稳定模块质量复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P3
State: done
Primary Module: x/observability/ops
Owned Files: `x/observability/ops/healthhttp/aggregate_regression_test.go`
Depends On: 无

Goal: 结构与 HTTP 状态精确固定，后续首次正常检查仍能更新报告。
Scope: 在真实报告入口观测无注册与注册后未检查两组。
Non-goals: 维持稳定公开签名与标准库依赖，将验证限于本卡范围。
Files: `x/observability/ops/healthhttp/aggregate_regression_test.go`。
Tests: 结构与 HTTP 状态精确固定，后续首次正常检查仍能更新报告。
Docs Sync: 记录核验结论；只在现有契约文字需要澄清时另提文档变更。
Done Definition: 完成下列验收，记录验证命令、状态及剩余限制。
Outcome: 无注册报告为 200；注册未检查的组件后返回 503；首次正常检查恢复 200 并新增一次统计。

## 证据与影响

第 9 节检查号为 6,16,31,42，当前证据状态为补证。 [质量规则第 9 节](../../../docs/operations/agent-code-quality-rules.md#L267)

注册初始化组件健康值，报告还会汇总其他状态。需区分空集与存在一个尚未检查的组件。 [x/observability/ops/healthhttp/manager.go:148](../../../x/observability/ops/healthhttp/manager.go#L148)

[复审报告](../../../docs/evidence/stable-quality-review-999d5e4d.md)

## 验收与验证

- [x] 结构与 HTTP 状态精确固定，后续首次正常检查仍能更新报告。
- [x] 记录挂载入口、具体断言和结果。已有测试能覆盖时复用并注明。

```sh
go test -race -timeout 60s ./x/observability/ops/healthhttp
```

## 实施证据

- [TestHealthReportDistinguishesEmptyAndUncheckedComponent](../../../x/observability/ops/healthhttp/aggregate_regression_test.go#L318)
