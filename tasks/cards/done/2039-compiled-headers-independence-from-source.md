# Card 2039 — 安全策略编译后与配置副本隔离

Milestone: 稳定模块第 9 节复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: security/headers
Owned Files: `security/headers/headers_test.go`
Depends On: 无

Goal: 安全策略编译后与配置副本隔离
Scope: 编译普通测试安全头策略，随后改动原结构体和嵌套配置，分别应用。
Non-goals: 范围限于本卡涉及的契约，保持公开签名和依赖。
Files: `security/headers/headers_test.go`。
Tests: 编译实例使用其原始值，显式更新只有重新编译才生效。
Docs Sync: 核对新增验收与已有模块说明一致，记录证据和限制。
Done Definition: 完成以下验收，记录命令、结果与剩余限制。
Outcome: 原始配置标量与附加头改变后，已编译实例仍用旧值，重新编译才应用新值。HSTS 指针隔离通过。

## 证据与影响

证据状态为补证。第 9 节检查号为 14,34,40,45。 [质量规则第 9 节](../../../docs/operations/agent-code-quality-rules.md#L267)

编译阶段固化头值，构造装配与按请求输出之间需要快照证据。 [security/headers/headers.go:184](../../../security/headers/headers.go#L184)

[复审报告](../../../docs/evidence/stable-quality-review-c3a77410.md)

## 验收与验证

- [x] 编译实例使用其原始值，显式更新只有重新编译才生效。
- [x] 已有测试覆盖相同触发和断言时可直接复用，记录具体用例。

```sh
go test -race -timeout 60s ./security/headers
```

## 实施证据

- [TestCompiledPolicyDetachesAdditionalHeaders](../../../security/headers/headers_test.go#L669)
- [TestCompileDetachesHSTSOptions](../../../security/headers/headers_test.go#L14)
