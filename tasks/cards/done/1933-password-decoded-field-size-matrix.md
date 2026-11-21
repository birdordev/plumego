# Card 1933 — 核对持久密码字段解码失败的固定错误分类

Milestone: 稳定模块质量复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: security
Owned Files: `security/password/password_test.go`
Depends On: 无

Goal: 检测在预期阶段结束，外层不回显原始字段或返回验证成功。
Scope: 只使用无凭据的人造持久字段，覆盖边界大小、普通无效编码和不合法长度；比较现有安全错误。
Non-goals: 维持稳定公开签名与标准库依赖，将验证限于本卡范围。
Files: `security/password/password_test.go`。
Tests: 检测在预期阶段结束，外层不回显原始字段或返回验证成功。
Docs Sync: 记录核验结论；只在现有契约文字需要澄清时另提文档变更。
Done Definition: 完成下列验收，记录验证命令、状态及剩余限制。
Outcome: 无凭据的持久字段夹具验证无效编码、超长和错误长度均返回 ErrInvalidHash，默认诊断不回显输入。

## 证据与影响

第 9 节检查号为 18,22,25,35,40。证据状态为补证。

字段在解码前后按大小拒绝。需核对公开验证入口面对无效编码和超限普通字面值时的分类。 [security/password/password.go:315](../../../security/password/password.go#L315)

[复审报告](../../../docs/evidence/stable-quality-review-2b94cf86.md)

## 验收与验证

- [x] 检测在预期阶段结束，外层不回显原始字段或返回验证成功。
- [x] 记录挂载入口、具体断言和结果。已有测试能覆盖时复用并注明。

```sh
go test -race -timeout 60s ./security/password
```

## 实施证据

- [TestPasswordStoredFieldRejectionKeepsFixedClassification](../../../security/password/password_test.go#L254)

[批次验证与基准](../review-2b94cf86/README.md)
