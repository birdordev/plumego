# Card 2054 — 未就绪组件 false 在 JSON 中保留

Milestone: 稳定模块第 9 节复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P3
State: done
Primary Module: health
Owned Files: `health/json_contract_test.go`
Depends On: 无

Goal: 未就绪组件 false 在 JSON 中保留
Scope: 对含 true 和 false 的非空 map 做 JSON 往返。
Non-goals: 范围限于本卡涉及的契约，保持公开签名和依赖。
Files: `health/json_contract_test.go`。
Tests: false 项存在，Ready 标签独立按输入保留。
Docs Sync: 核对新增验收与已有模块说明一致，记录证据和限制。
Done Definition: 完成以下验收，记录命令、结果与剩余限制。
Outcome: JSON 反序列化仍保留显式 false 组件，整体 Ready 仍按原来的独立值。

## 证据与影响

证据状态为补证。第 9 节检查号为 6,38,42,45。 [质量规则第 9 节](../../../docs/operations/agent-code-quality-rules.md#L267)

Components 使用 omitempty 只作用于整个 map，false 组件项需要保留。 [health/readiness.go:14](../../../health/readiness.go#L14)

[复审报告](../../../docs/evidence/stable-quality-review-c3a77410.md)

## 验收与验证

- [x] false 项存在，Ready 标签独立按输入保留。
- [x] 已有测试覆盖相同触发和断言时可直接复用，记录具体用例。

```sh
go test -race -timeout 60s ./health
```

## 实施证据

- [TestFalseReadinessComponentSurvivesJSONRoundTrip](../../../health/json_contract_test.go#L355)
