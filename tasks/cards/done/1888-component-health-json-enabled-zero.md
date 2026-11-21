# Card 1888 — 核对组件健康模型 Enabled 的零值序列化

Milestone: 稳定模块质量复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P3
State: done
Primary Module: health
Owned Files: `health/json_contract_test.go`
Depends On: 无

Goal: 保留嵌入 JSON 形状、布尔区分、零值省略规则，宿主无新增语义。
Scope: 以 ComponentHealth 三组公开值序列化对比字段与状态枚举。
Non-goals: 维持稳定公开签名与标准库依赖，将验证限于本卡范围。
Files: `health/json_contract_test.go`。
Tests: 保留嵌入 JSON 形状、布尔区分、零值省略规则，宿主无新增语义。
Docs Sync: 记录核验结论；只在现有契约文字需要澄清时另提文档变更。
Done Definition: 完成下列验收，记录验证命令、状态及剩余限制。
Outcome: 组件健康 JSON 展平状态并始终包含 Enabled，空依赖沿用省略。

## 证据与影响

第 9 节检查号为 6,16,38,42。证据状态为补证。

组件模型嵌入 HealthStatus，Enabled 即使为 false 也显式输出。需组合零值、有效状态和空依赖。 [health/core.go:40](../../../health/core.go#L40)

[复审报告](../../../docs/evidence/stable-quality-review-a8c2f28a.md)

## 验收与验证

- [x] 保留嵌入 JSON 形状、布尔区分、零值省略规则，宿主无新增语义。
- [x] 记录挂载入口、具体断言和结果。已有测试能覆盖时复用并注明。

```sh
go test -race -timeout 60s ./health
```

## 实施证据

- [TestComponentHealthJSONEmbedsStateAndExplicitEnabled](../../../health/json_contract_test.go#L274)

[批次验证与基准](../review-a8c2f28a/README.md)
