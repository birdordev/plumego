# Card 2079 — 同一请求重试编码保持相关 ID

Milestone: 稳定模块第 9 节复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: contract
Owned Files: `contract/response_test.go`
Depends On: 无

Goal: 同一请求重试编码保持相关 ID
Scope: 带固定相关 ID 用失败 Meta 触发编码退出，再发送公开错误响应。
Non-goals: 范围限于本卡涉及的契约，保持公开签名和依赖。
Files: `contract/response_test.go`。
Tests: 首次无最终状态，第二次仅一次提交，JSON request_id 与原上下文一致。
Docs Sync: 核对新增验收与已有模块说明一致，记录证据和限制。
Done Definition: 完成以下验收，记录命令、结果与剩余限制。
Outcome: 标准响应编码失败保持未提交，再从同一请求写规范错误时相关 ID 保留并仅提交一次。

## 证据与影响

证据状态为补证。第 9 节检查号为 21,23,27,38,46。 [质量规则第 9 节](../../../docs/operations/agent-code-quality-rules.md#L267)

成功包体从上下文读取请求 ID，失败编码和第二次成功应该保持一致。 [contract/response.go:56](../../../contract/response.go#L56)

[复审报告](../../../docs/evidence/stable-quality-review-2f962d21.md)

## 验收与验证

- [x] 首次无最终状态，第二次仅一次提交，JSON request_id 与原上下文一致。
- [x] 已有测试覆盖相同触发和断言时可直接复用，记录具体用例。

```sh
go test -race -timeout 60s ./contract
```

## 实施证据

- [TestRequestIDPreservedInErrorAfterUncommittedEncodingFailure](../../../contract/response_test.go#L973)
