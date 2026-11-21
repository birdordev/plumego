# Card 2107 — 未知状态值和依赖 JSON 往返

Milestone: 稳定模块第 9 节复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P3
State: done
Primary Module: health
Owned Files: `health/json_contract_test.go`
Depends On: 无

Goal: 未知状态值和依赖 JSON 往返
Scope: 对含非空依赖和未知值的模型往返反序列化并重新判定。
Non-goals: 范围限于本卡涉及的契约，保持公开签名和依赖。
Files: `health/json_contract_test.go`。
Tests: 未知字面值和依赖列表保留，两个判定均为 false。
Docs Sync: 核对新增验收与已有模块说明一致，记录证据和限制。
Done Definition: 完成以下验收，记录命令、结果与剩余限制。
Outcome: 未知状态与非空依赖 JSON 往返保留，已知与就绪判定均为 false。

## 证据与影响

证据状态为补证。第 9 节检查号为 6,16,38,42。 [质量规则第 9 节](../../../docs/operations/agent-code-quality-rules.md#L267)

类型允许传输未知状态，但 IsReady 和 IsKnown 要关闭。 [health/core.go:68](../../../health/core.go#L68)

[复审报告](../../../docs/evidence/stable-quality-review-2f962d21.md)

## 验收与验证

- [x] 未知字面值和依赖列表保留，两个判定均为 false。
- [x] 已有测试覆盖相同触发和断言时可直接复用，记录具体用例。

```sh
go test -race -timeout 60s ./health
```

## 实施证据

- [TestUnknownHealthStateAndDependenciesRoundTrip](../../../health/json_contract_test.go#L370)
