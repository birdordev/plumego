# Card 1878 — 核对口令强度字符计数和 UTF-8 可见长度

Milestone: 稳定模块质量复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: security
Owned Files: `security/password/password_test.go`
Depends On: 无

Goal: 长度单位明确，Unicode 分类与配置一致，过界输入结果固定。
Scope: 生成普通测试字符串，按检查项逐个触发最短或最长边界，本机执行，不使用真实凭据。
Non-goals: 维持稳定公开签名与标准库依赖，将验证限于本卡范围。
Files: `security/password/password_test.go`。
Tests: 长度单位明确，Unicode 分类与配置一致，过界输入结果固定。
Docs Sync: 记录核验结论；只在现有契约文字需要澄清时另提文档变更。
Done Definition: 完成下列验收，记录验证命令、状态及剩余限制。
Outcome: 强度配置按 Unicode 字符计长度，合成大小写、数字、符号分别验证。

## 证据与影响

第 9 节检查号为 6,14,18,40。证据状态为补证。

强度配置涉及长度和字符分类，字节长度上限属于独立哈希边界。需要文档化多字节合成字符串的结果。 [security/password/password.go:145](../../../security/password/password.go#L145)

[复审报告](../../../docs/evidence/stable-quality-review-a8c2f28a.md)

## 验收与验证

- [x] 长度单位明确，Unicode 分类与配置一致，过界输入结果固定。
- [x] 记录挂载入口、具体断言和结果。已有测试能覆盖时复用并注明。

```sh
go test -race -timeout 60s ./security/password
```

## 实施证据

- [TestPasswordStrengthUsesUnicodeCharacterCounts](../../../security/password/password_test.go#L240)

[批次验证与基准](../review-a8c2f28a/README.md)
