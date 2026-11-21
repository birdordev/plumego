# Card 2043 — 普通邮箱标签边界契约

Milestone: 稳定模块第 9 节复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P3
State: done
Primary Module: security/input
Owned Files: `security/input/input_test.go`
Depends On: 无

Goal: 普通邮箱标签边界契约
Scope: 构造普通 ASCII 标签在允许上界与上界加一的地址。
Non-goals: 范围限于本卡涉及的契约，保持公开签名和依赖。
Files: `security/input/input_test.go`。
Tests: 合法边界被接受，越界被拒绝，前后空白处理保持已有兼容。
Docs Sync: 核对新增验收与已有模块说明一致，记录证据和限制。
Done Definition: 完成以下验收，记录命令、结果与剩余限制。
Outcome: 普通 ASCII 标签验证 63 和 64 字节边界，并保留周边空白兼容。

## 证据与影响

证据状态为补证。第 9 节检查号为 14,18,40,46。 [质量规则第 9 节](../../../docs/operations/agent-code-quality-rules.md#L267)

邮箱验证声明域名标签规则。只需核查常规字符构成的长度边界。 [security/input/input.go:169](../../../security/input/input.go#L169)

[复审报告](../../../docs/evidence/stable-quality-review-c3a77410.md)

## 验收与验证

- [x] 合法边界被接受，越界被拒绝，前后空白处理保持已有兼容。
- [x] 已有测试覆盖相同触发和断言时可直接复用，记录具体用例。

```sh
go test -race -timeout 60s ./security/input
```

## 实施证据

- [TestValidateEmailLocalAndDomainLabelByteLimits](../../../security/input/input_test.go#L509)
