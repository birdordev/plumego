# Card 2026 — 多分支 APIError 的选择次序

Milestone: 稳定模块第 9 节复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: contract
Owned Files: `contract/errors_chain_test.go`
Depends On: 无

Goal: 多分支 APIError 的选择次序
Scope: 构造两端状态不同的 errors.Join，交换值和指针的位置。
Non-goals: 范围限于本卡涉及的契约，保持公开签名和依赖。
Files: `contract/errors_chain_test.go`。
Tests: 记录 AsAPIError 的可重复选择顺序，确认重复选择未污染源错误。
Docs Sync: 核对新增验收与已有模块说明一致，记录证据和限制。
Done Definition: 完成以下验收，记录命令、结果与剩余限制。
Outcome: 交换并列值与指针的位置并重复选择，首个值优先，源错误状态维持原值。

## 证据与影响

证据状态为补证。第 9 节检查号为 6,22,38,46。 [质量规则第 9 节](../../../docs/operations/agent-code-quality-rules.md#L267)

错误链同时支持值和指针，以及多子错误。需要明确并列分支的先后。 [contract/errors.go:370](../../../contract/errors.go#L370)

[复审报告](../../../docs/evidence/stable-quality-review-c3a77410.md)

## 验收与验证

- [x] 记录 AsAPIError 的可重复选择顺序，确认重复选择未污染源错误。
- [x] 已有测试覆盖相同触发和断言时可直接复用，记录具体用例。

```sh
go test -race -timeout 60s ./contract
```

## 实施证据

- [TestAsAPIErrorSharedBranchesChooseFirstValue](../../../contract/errors_chain_test.go#L229)
