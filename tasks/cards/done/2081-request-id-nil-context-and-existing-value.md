# Card 2081 — nil 与已有请求 ID 的访问器语义

Milestone: 稳定模块第 9 节复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P3
State: done
Primary Module: contract
Owned Files: `contract/request_id_test.go`
Depends On: 无

Goal: nil 与已有请求 ID 的访问器语义
Scope: 先从 nil 构造，随后测试全空白输入和一个新合法输入。
Non-goals: 范围限于本卡涉及的契约，保持公开签名和依赖。
Files: `contract/request_id_test.go`。
Tests: 读取使用兼容规则，显式更新成功且父上下文的值不变。
Docs Sync: 核对新增验收与已有模块说明一致，记录证据和限制。
Done Definition: 完成以下验收，记录命令、结果与剩余限制。
Outcome: 覆盖 nil 上下文、空白拒绝、有效替换和父上下文隔离。

## 证据与影响

证据状态为补证。第 9 节检查号为 16,27,38,45。 [质量规则第 9 节](../../../docs/operations/agent-code-quality-rules.md#L267)

访问器可规范化 nil 上下文与普通相关值。 [contract/request_id.go:23](../../../contract/request_id.go#L23)

[复审报告](../../../docs/evidence/stable-quality-review-2f962d21.md)

## 验收与验证

- [x] 读取使用兼容规则，显式更新成功且父上下文的值不变。
- [x] 已有测试覆盖相同触发和断言时可直接复用，记录具体用例。

```sh
go test -race -timeout 60s ./contract
```

## 实施证据

- [TestRequestIDWithNilContext](../../../contract/request_id_test.go#L73)
- [TestRequestIDRejectedReplacementPreservesExistingID](../../../contract/request_id_test.go#L63)
