# Card 2082 — 无正文状态对现有响应头的提交边界

Milestone: 稳定模块第 9 节复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: contract
Owned Files: `contract/response_test.go`
Depends On: 无

Goal: 无正文状态对现有响应头的提交边界
Scope: 为两者分别添加普通扩展头、可能不适用的 framing 头和可计数值。
Non-goals: 范围限于本卡涉及的契约，保持公开签名和依赖。
Files: `contract/response_test.go`。
Tests: 用户编码器无调用，扩展头与提交次数遵循当前契约。
Docs Sync: 核对新增验收与已有模块说明一致，记录证据和限制。
Done Definition: 完成以下验收，记录命令、结果与剩余限制。
Outcome: 204 与 205 均只提交一次，用户编码器零调用，普通头保留，长度和 framing 按各自约定。

## 证据与影响

证据状态为补证。第 9 节检查号为 21,23,24,38。 [质量规则第 9 节](../../../docs/operations/agent-code-quality-rules.md#L267)

204 与 205 对内容相关头处理不同，现有测试覆盖部分组合。 [contract/response.go:22](../../../contract/response.go#L22)

[复审报告](../../../docs/evidence/stable-quality-review-2f962d21.md)

## 验收与验证

- [x] 用户编码器无调用，扩展头与提交次数遵循当前契约。
- [x] 已有测试覆盖相同触发和断言时可直接复用，记录具体用例。

```sh
go test -race -timeout 60s ./contract
```

## 实施证据

- [TestBodylessResponseKeepsUnrelatedHeaders](../../../contract/response_test.go#L730)
- [TestWriteResponseBodylessFramingBeforeTransport](../../../contract/response_test.go#L711)
