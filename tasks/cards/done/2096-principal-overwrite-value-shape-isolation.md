# Card 2096 — 请求上下文替换 Principal 时父快照隔离

Milestone: 稳定模块第 9 节复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P3
State: done
Primary Module: security/authn
Owned Files: `security/authn/authn_test.go`
Depends On: 无

Goal: 请求上下文替换 Principal 时父快照隔离
Scope: 从一个占位主体派生不同角色，改写父和子读取出的字段。
Non-goals: 范围限于本卡涉及的契约，保持公开签名和依赖。
Files: `security/authn/authn_test.go`。
Tests: 父子保持各自值，后续读取仍按公开快照规则。
Docs Sync: 核对新增验收与已有模块说明一致，记录证据和限制。
Done Definition: 完成以下验收，记录命令、结果与剩余限制。
Outcome: 从父上下文替换主体后，修改父子读取副本的角色与 claim 都不会改动原快照。

## 证据与影响

证据状态为补证。第 9 节检查号为 27,34,40,45。 [质量规则第 9 节](../../../docs/operations/agent-code-quality-rules.md#L267)

WithPrincipal 在派生上下文可替换父值。需核验两份常规角色和 claim 的独立性。 [security/authn/authn.go:48](../../../security/authn/authn.go#L48)

[复审报告](../../../docs/evidence/stable-quality-review-2f962d21.md)

## 验收与验证

- [x] 父子保持各自值，后续读取仍按公开快照规则。
- [x] 已有测试覆盖相同触发和断言时可直接复用，记录具体用例。

```sh
go test -race -timeout 60s ./security/authn
```

## 实施证据

- [TestWithPrincipalOverwrite](../../../security/authn/authn_test.go#L133)
