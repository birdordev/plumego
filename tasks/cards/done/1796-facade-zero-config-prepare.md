# Card 1796 — 核对根门面显式零配置与默认配置的文档边界

Milestone: 稳定模块质量复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P3
State: done
Primary Module: .
Owned Files: `plumego_test.go`
Depends On: 无

Goal: 根门面职责不扩展，失败可定位到所有者。
Scope: 使用两个构造入口对照准备及公开签名。
Non-goals: 维持稳定公开签名与标准库依赖，将验证限于本卡范围。
Files: `plumego_test.go`。
Tests: 根门面职责不扩展，失败可定位到所有者。
Docs Sync: 复核本卡涉及的已公开约定。
Done Definition: 完成下列验收，记录验证命令、状态及剩余限制。
Outcome: 显式零配置由 core 验证地址，与 New 使用默认配置的行为分别核对，根门面保持现有职责。

## 证据与影响

第 9 节检查号为 3,6,14,16。证据状态为补证。

门面分别提供 New 和显式 NewWithConfig。零值的行为依赖 core 的验证，不应由门面隐式填充。 [plumego.go:60](../../../plumego.go#L60)

[复审报告](../../../docs/evidence/stable-quality-review-d224acbc.md)

## 验收与验证

- [x] 根门面职责不扩展，失败可定位到所有者。
- [x] 记录运行结果。若已有用例覆盖目标，记录用例及相关断言。

```sh
go test -race -timeout 60s .
```

## 实施证据

- [TestFacadeExplicitZeroConfigKeepsCoreValidation](../../../plumego_test.go#L189)

[批次验证与基准](../review-d224acbc/README.md)
