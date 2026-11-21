# Card 2094 — 密码强度配置零值的公开行为

Milestone: 稳定模块第 9 节复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P3
State: done
Primary Module: security/password
Owned Files: `security/password/password_strength_test.go`, `security/password/password.go`
Depends On: 无

Goal: 密码强度配置零值的公开行为
Scope: 使用合成短字符串验证零值、显式长度及启用一个字符类别的组合。
Non-goals: 范围限于本卡涉及的契约，保持公开签名和依赖。
Files: `security/password/password_strength_test.go`, `security/password/password.go`。
Tests: 记录现行布尔输出，在文档明确零值选择。
Docs Sync: 已补充 ValidatePasswordStrength 的零配置语义。
Done Definition: 完成以下验收，记录命令、结果与剩余限制。
Outcome: 验证零值不施加强度要求，显式字符分类与长度各自生效。GoDoc 提示调用方显式采用默认策略。

## 证据与影响

证据状态为补证。第 9 节检查号为 6,14,40,46。 [质量规则第 9 节](../../../docs/operations/agent-code-quality-rules.md#L267)

强度配置归一化和空用户输入本来分离。 [security/password/password.go:147](../../../security/password/password.go#L147)

[复审报告](../../../docs/evidence/stable-quality-review-2f962d21.md)

## 验收与验证

- [x] 记录现行布尔输出，在文档明确零值选择。
- [x] 已有测试覆盖相同触发和断言时可直接复用，记录具体用例。

```sh
go test -race -timeout 60s ./security/password
```

## 实施证据

- [TestStrengthZeroValueAndExplicitSingleRequirement](../../../security/password/password_strength_test.go#L350)
