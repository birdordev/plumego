# Card 2032 — 关闭请求头注入时的传播所有权

Milestone: 稳定模块第 9 节复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: middleware/requestid
Owned Files: `middleware/requestid/request_id_test.go`
Depends On: 无

Goal: 关闭请求头注入时的传播所有权
Scope: 关闭注入后使用确定性生成器和原始空头，让 next 比较三处数据。
Non-goals: 范围限于本卡涉及的契约，保持公开签名和依赖。
Files: `middleware/requestid/request_id_test.go`。
Tests: 原请求头不变，上下文与响应头同 ID，生成一次。
Docs Sync: 核对新增验收与已有模块说明一致，记录证据和限制。
Done Definition: 完成以下验收，记录命令、结果与剩余限制。
Outcome: 禁用请求头写入时调用生成器一次，原请求不被修改，上下文与响应相关 ID 一致。

## 证据与影响

证据状态为补证。第 9 节检查号为 6,18,27,34,39。 [质量规则第 9 节](../../../docs/operations/agent-code-quality-rules.md#L267)

选定 ID 会写入上下文与响应，只有配置开启时才复制注入请求头。 [middleware/requestid/request_id.go:32](../../../middleware/requestid/request_id.go#L32)

[复审报告](../../../docs/evidence/stable-quality-review-c3a77410.md)

## 验收与验证

- [x] 原请求头不变，上下文与响应头同 ID，生成一次。
- [x] 已有测试覆盖相同触发和断言时可直接复用，记录具体用例。

```sh
go test -race -timeout 60s ./middleware/requestid
```

## 实施证据

- [TestRequestHeaderOptOutStillPropagatesGeneratedID](../../../middleware/requestid/request_id_test.go#L226)
