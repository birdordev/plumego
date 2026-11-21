# Card 2106 — 健康 Details 的具名容器浅复制

Milestone: 稳定模块第 9 节复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P3
State: done
Primary Module: health
Owned Files: `health/health_test.go`
Depends On: 无

Goal: 健康 Details 的具名容器浅复制
Scope: 变更 clone 的顶层和嵌套字段，分别观察两处。
Non-goals: 范围限于本卡涉及的契约，保持公开签名和依赖。
Files: `health/health_test.go`。
Tests: 顶层独立，嵌套别名按文档存在，模型 JSON 保持可用。
Docs Sync: 核对新增验收与已有模块说明一致，记录证据和限制。
Done Definition: 完成以下验收，记录命令、结果与剩余限制。
Outcome: 使用具名嵌套 map 双向确认浅复制约定，顶层快照独立且 JSON 仍可编码。

## 证据与影响

证据状态为补证。第 9 节检查号为 16,34,42,45。 [质量规则第 9 节](../../../docs/operations/agent-code-quality-rules.md#L267)

Clone 说明只复制外层容器。用具名嵌套 map 与不可变普通值验证契约。 [health/core.go:45](../../../health/core.go#L45)

[复审报告](../../../docs/evidence/stable-quality-review-2f962d21.md)

## 验收与验证

- [x] 顶层独立，嵌套别名按文档存在，模型 JSON 保持可用。
- [x] 已有测试覆盖相同触发和断言时可直接复用，记录具体用例。

```sh
go test -race -timeout 60s ./health
```

## 实施证据

- [TestHealthClonesKeepIndependentTopLevelAndSharedNestedValues](../../../health/health_test.go#L181)
