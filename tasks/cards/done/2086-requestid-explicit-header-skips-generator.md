# Card 2086 — 合法已有相关 ID 时跳过生成回调

Milestone: 稳定模块第 9 节复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P3
State: done
Primary Module: middleware/requestid
Owned Files: `middleware/requestid/request_id_test.go`
Depends On: 无

Goal: 合法已有相关 ID 时跳过生成回调
Scope: 用会计数的生成器搭配合法来值与未启用写回选项。
Non-goals: 范围限于本卡涉及的契约，保持公开签名和依赖。
Files: `middleware/requestid/request_id_test.go`。
Tests: 生成器始终零调用，响应与上下文已有值一致。
Docs Sync: 核对新增验收与已有模块说明一致，记录证据和限制。
Done Definition: 完成以下验收，记录命令、结果与剩余限制。
Outcome: 合法来值且关闭请求头写回时，生成器零调用，相关 ID 贯通上下文及响应。

## 证据与影响

证据状态为补证。第 9 节检查号为 18,20,27,39。 [质量规则第 9 节](../../../docs/operations/agent-code-quality-rules.md#L267)

已有用户传入的合法关联标识时应避免生成副作用。 [middleware/requestid/helpers.go:47](../../../middleware/requestid/helpers.go#L47)

[复审报告](../../../docs/evidence/stable-quality-review-2f962d21.md)

## 验收与验证

- [x] 生成器始终零调用，响应与上下文已有值一致。
- [x] 已有测试覆盖相同触发和断言时可直接复用，记录具体用例。

```sh
go test -race -timeout 60s ./middleware/requestid
```

## 实施证据

- [TestValidInboundRequestIDSkipsGeneratorWithWritebackDisabled](../../../middleware/requestid/request_id_test.go#L245)
