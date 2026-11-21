# Card 2030 — BodyLimit 禁用时保留请求身份

Milestone: 稳定模块第 9 节复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P3
State: done
Primary Module: middleware/bodylimit
Owned Files: `middleware/bodylimit/body_limit_test.go`
Depends On: 无

Goal: BodyLimit 禁用时保留请求身份
Scope: 按零和负阈值运行两个普通请求。
Non-goals: 范围限于本卡涉及的契约，保持公开签名和依赖。
Files: `middleware/bodylimit/body_limit_test.go`。
Tests: next 恰好一次并收到原对象，没有限额诊断。
Docs Sync: 核对新增验收与已有模块说明一致，记录证据和限制。
Done Definition: 完成以下验收，记录命令、结果与剩余限制。
Outcome: 零与负阈值均直接传递原请求、writer 和 body，只调用一次 next。

## 证据与影响

证据状态为补证。第 9 节检查号为 14,17,20,39。 [质量规则第 9 节](../../../docs/operations/agent-code-quality-rules.md#L267)

禁用分支在包装前直通，公开组合需要锁定原 request、writer 与 body 的身份。 [middleware/bodylimit/body_limit.go:44](../../../middleware/bodylimit/body_limit.go#L44)

[复审报告](../../../docs/evidence/stable-quality-review-c3a77410.md)

## 验收与验证

- [x] next 恰好一次并收到原对象，没有限额诊断。
- [x] 已有测试覆盖相同触发和断言时可直接复用，记录具体用例。

```sh
go test -race -timeout 60s ./middleware/bodylimit
```

## 实施证据

- [TestMiddlewareDisabledPassesThrough](../../../middleware/bodylimit/body_limit_test.go#L192)
