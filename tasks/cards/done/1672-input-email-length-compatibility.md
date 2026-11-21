# Card 1672 — 补证规范邮件地址本地部分和域标签的边界

Milestone: 稳定模块质量复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: security
Owned Files: `security/input/input_test.go`
Depends On: 无

Goal: 补证规范邮件地址本地部分和域标签的边界。
Scope: 组合最短、合法边界及边界外域标签长度的合成输入。
Non-goals: 维持公开签名和依赖；涉及现有兼容契约的调整须先核对调用边界及迁移影响。
Files: `security/input/input_test.go`。
Tests: 记录 Unicode 与 ASCII 行为，保持“基本检查”的输出约定。
Docs Sync: 对照原契约核验，若证据要求修订公开行为，先更新实施范围。
Done Definition: 完成测试与验收记录，并标明剩余取证范围。
Outcome: 覆盖邮件本地部分 64/65 字节、域标签 63/64 字节与非 ASCII 域规则。

## 证据

检查号为 6,14,18,40，状态为补证。

长度按字节，并要求标准解析后的地址完全等于原输入。 [security/input/input.go:187](../../../security/input/input.go#L187)

[复审报告](../../../docs/evidence/stable-quality-review-09a7f5d3.md)

## 验收与验证

- [x] 记录 Unicode 与 ASCII 行为，保持“基本检查”的输出约定。
- [x] 记录验证命令、状态和剩余范围。若使用既有用例满足验收，记录对应证据。

```sh
go test -race -timeout 60s ./security/input
```

## 实施证据

[TestValidateEmailLocalAndDomainLabelByteLimits](../../../security/input/input_test.go#L509)
