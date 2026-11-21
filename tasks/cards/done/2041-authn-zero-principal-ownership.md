# Card 2041 — 零 Principal 多次读取的独立性

Milestone: 稳定模块第 9 节复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P3
State: done
Primary Module: security/authn
Owned Files: `security/authn/authn_test.go`
Depends On: 无

Goal: 零 Principal 多次读取的独立性
Scope: 存入含非 nil 空容器的测试主体，重复获取然后分别改写可写字段。
Non-goals: 范围限于本卡涉及的契约，保持公开签名和依赖。
Files: `security/authn/authn_test.go`。
Tests: Subject、Roles 和 Attributes 的后续读取保持初始形状。
Docs Sync: 核对新增验收与已有模块说明一致，记录证据和限制。
Done Definition: 完成以下验收，记录命令、结果与剩余限制。
Outcome: 空可选容器保留形状，修改读取到的主题、权限列表和声明不改变存储主体。

## 证据与影响

证据状态为补证。第 9 节检查号为 27,34,40,45。 [质量规则第 9 节](../../../docs/operations/agent-code-quality-rules.md#L267)

认证上下文会复制主体。空可选字段的所有权应从公开 accessors 验证。 [security/authn/authn.go:69](../../../security/authn/authn.go#L69)

[复审报告](../../../docs/evidence/stable-quality-review-c3a77410.md)

## 验收与验证

- [x] Subject、Roles 和 Attributes 的后续读取保持初始形状。
- [x] 已有测试覆盖相同触发和断言时可直接复用，记录具体用例。

```sh
go test -race -timeout 60s ./security/authn
```

## 实施证据

- [TestPrincipalSnapshotDistinguishesNilAndEmptyAttributes](../../../security/authn/authn_test.go#L177)
