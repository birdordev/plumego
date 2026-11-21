# Card 1993 — 核对组件零耗时的 JSON 往返

Milestone: 稳定模块质量复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P3
State: done
Primary Module: health
Owned Files: `health/json_contract_test.go`
Depends On: 无

Goal: 字段存在性、单位和回读值符合现有模型。
Scope: 对比零与最小正持续时间的编码、解码与 Clone。
Non-goals: 维持稳定公开签名与标准库依赖，将验证限于本卡范围。
Files: `health/json_contract_test.go`。
Tests: 字段存在性、单位和回读值符合现有模型。
Docs Sync: 记录核验结论；只在现有契约文字需要澄清时另提文档变更。
Done Definition: 完成下列验收，记录验证命令、状态及剩余限制。
Outcome: 预期零耗时省略和一纳秒数值在 ComponentHealth 编解码以及 Clone 时保持。

## 证据与影响

第 9 节检查号为 6,16,42,45，当前证据状态为补证。 [质量规则第 9 节](../../../docs/operations/agent-code-quality-rules.md#L267)

组件状态内嵌基础健康值并携带耗时。HealthStatus 已有纳秒单位测试，本卡聚焦嵌入 ComponentHealth 后的解码和 Clone。 [health/core.go:55](../../../health/core.go#L55)

[复审报告](../../../docs/evidence/stable-quality-review-999d5e4d.md)

## 验收与验证

- [x] 字段存在性、单位和回读值符合现有模型。
- [x] 记录挂载入口、具体断言和结果。已有测试能覆盖时复用并注明。

```sh
go test -race -timeout 60s ./health
```

## 实施证据

- [TestComponentDurationCloneJSONRoundTrip](../../../health/json_contract_test.go#L313)
