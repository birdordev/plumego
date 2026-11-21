# Card 2040 — 密码成本参数边界及分类

Milestone: 稳定模块第 9 节复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: security/password
Owned Files: `security/password/password_test.go`
Depends On: 无

Goal: 密码成本参数边界及分类
Scope: 利用已有限额，检查超边界参数被快速分类拒绝，并通过最低允许成本完成一次往返。
Non-goals: 范围限于本卡涉及的契约，保持公开签名和依赖。
Files: `security/password/password_test.go`。
Tests: 不支持值返回已声明错误且没有摘要，最低允许值可校验虚构密码。
Docs Sync: 核对新增验收与已有模块说明一致，记录证据和限制。
Done Definition: 完成以下验收，记录命令、结果与剩余限制。
Outcome: 高低越界返回可分类错误和空摘要，最低允许成本可校验虚构密码。

## 证据与影响

证据状态为补证。第 9 节检查号为 14,22,35,40,46。 [质量规则第 9 节](../../../docs/operations/agent-code-quality-rules.md#L267)

成本归一化和范围决定开销。边界需用占位输入核验，避免在测试执行昂贵样例。 [security/password/password.go:336](../../../security/password/password.go#L336)

[复审报告](../../../docs/evidence/stable-quality-review-c3a77410.md)

## 验收与验证

- [x] 不支持值返回已声明错误且没有摘要，最低允许值可校验虚构密码。
- [x] 已有测试覆盖相同触发和断言时可直接复用，记录具体用例。

```sh
go test -race -timeout 60s ./security/password
```

## 实施证据

- [TestHashPasswordWithCostInvalidCostTooLow](../../../security/password/password_test.go#L101)
- [TestHashPasswordWithCostInvalidCostTooHigh](../../../security/password/password_test.go#L116)
