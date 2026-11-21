# Card 2025 — 成功响应 Meta 失败时保持未提交

Milestone: 稳定模块第 9 节复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: contract
Owned Files: `contract/response_test.go`
Depends On: 无

Goal: 成功响应 Meta 失败时保持未提交
Scope: 为 Meta 放入失败编码器，再通过正常 API 写入有效响应。
Non-goals: 范围限于本卡涉及的契约，保持公开签名和依赖。
Files: `contract/response_test.go`。
Tests: 首次失败未提交并保留调用方头，第二次有效写入仅提交一次。
Docs Sync: 核对新增验收与已有模块说明一致，记录证据和限制。
Done Definition: 完成以下验收，记录命令、结果与剩余限制。
Outcome: 仅 Meta 编码失败时保留已有头和未提交状态，后续成功写入只提交一次。

## 证据与影响

证据状态为补证。第 9 节检查号为 6,21,23,38,46。 [质量规则第 9 节](../../../docs/operations/agent-code-quality-rules.md#L267)

成功包体与元数据一起编码。元数据失败时应保留外层处理错误的机会。 [contract/response.go:56](../../../contract/response.go#L56)

[复审报告](../../../docs/evidence/stable-quality-review-c3a77410.md)

## 验收与验证

- [x] 首次失败未提交并保留调用方头，第二次有效写入仅提交一次。
- [x] 已有测试覆盖相同触发和断言时可直接复用，记录具体用例。

```sh
go test -race -timeout 60s ./contract
```

## 实施证据

- [TestWriteResponseMetaFailureLeavesResponseOpen](../../../contract/response_test.go#L954)
