# Card 1608 — 补证编译安全头策略与源配置隔离

Milestone: 本轮质量审查
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: security
Owned Files: `security/headers/headers.go`, `security/headers/headers_test.go`, `middleware/securityheaders/headers.go`, `docs/modules/security/README.md`
Depends On: 无

Goal: 补证编译安全头策略与源配置隔离。
Scope: 先核对 Compile 的复制承诺，再编写对应的输入变更测试。
Non-goals: 保留现有公开签名、依赖与模块边界。测试若证实新的缺陷，先记录证据再单独安排修复。
Files: `security/headers/headers.go`, `security/headers/headers_test.go`, `middleware/securityheaders/headers.go`, `docs/modules/security/README.md`。
Tests: 对默认值、覆盖优先级和 Additional 源配置变化均有可重复断言。
Docs Sync: 本卡按现有契约验证。若取证表明契约需调整，先更新后续卡范围。
Done Definition: 完成下列验收和验证，记录证据与剩余风险。
Outcome: Compile 在所属模块复制 Additional，安全头中间件复用该路径。验证源配置变更不会修改编译后响应。

## 证据与影响

- 检查号：14,15,34,40。证据状态：补证。
- 策略编译用于中间件配置固化，Additional 字段所有权需要直接消费者证据。 [security/headers/headers.go:183](../../../security/headers/headers.go#L183)
- [审查报告](../../../docs/evidence/stable-quality-review-dee7aa0f.md)

## 验收与验证

- [x] 对默认值、覆盖优先级和 Additional 源配置变化均有可重复断言。
- [x] 运行以下命令并记录结果、失败路径和未验证范围。修改 Go 文件前完成仓库预检，验证前先执行仓库格式化。

```sh
go test -race -timeout 60s ./security/headers
```

## 实施证据

[TestCompiledPolicyDetachesAdditionalHeaders](../../../security/headers/headers_test.go#L668)
