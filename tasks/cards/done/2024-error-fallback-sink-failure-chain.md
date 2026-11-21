# Card 2024 — 编码回退时保留底层写入错误

Milestone: 稳定模块第 9 节复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: contract
Owned Files: `contract/response_test.go`
Depends On: 无

Goal: 编码回退时保留底层写入错误
Scope: 令详情 marshaler 返回哨兵，并用记录最终状态的错误 writer。
Non-goals: 范围限于本卡涉及的契约，保持公开签名和依赖。
Files: `contract/response_test.go`。
Tests: 两个 errors.Is 均为真，至多一个最终状态，无额外回退写入。
Docs Sync: 核对新增验收与已有模块说明一致，记录证据和限制。
Done Definition: 完成以下验收，记录命令、结果与剩余限制。
Outcome: 复用编码失败、部分写入与 Join 回归。两种错误可分类，响应一次提交且回退只尝试一次。

## 证据与影响

证据状态为补证。第 9 节检查号为 21,22,23,38,46。 [质量规则第 9 节](../../../docs/operations/agent-code-quality-rules.md#L267)

错误详情编码失败后再遇底层写入错误时走 Join 分支，需要同时验证返回的两类错误和提交次数。 [contract/response.go:143](../../../contract/response.go#L143)

[复审报告](../../../docs/evidence/stable-quality-review-c3a77410.md)

## 验收与验证

- [x] 两个 errors.Is 均为真，至多一个最终状态，无额外回退写入。
- [x] 已有测试覆盖相同触发和断言时可直接复用，记录具体用例。

```sh
go test -race -timeout 60s ./contract
```

## 实施证据

- [TestErrorDetailFallbackJoinsEncodingAndWriterFailures](../../../contract/response_test.go#L919)
- [TestResponseWritersReturnWriteFailureWithoutRetry](../../../contract/response_test.go#L385)
