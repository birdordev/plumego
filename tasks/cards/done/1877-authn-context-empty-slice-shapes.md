# Card 1877 — 补证 Principal 快照的 nil 与空集合形状

Milestone: 稳定模块质量复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: security
Owned Files: `security/authn/authn_test.go`
Depends On: 无

Goal: 容器形状符合已有契约，编排授权入口不受影响。
Scope: 合成普通主体，测试 nil 与显式空 Roles、Scopes、Claims，再读取独立快照。
Non-goals: 维持稳定公开签名与标准库依赖，将验证限于本卡范围。
Files: `security/authn/authn_test.go`。
Tests: 容器形状符合已有契约，编排授权入口不受影响。
Docs Sync: 记录核验结论；只在现有契约文字需要澄清时另提文档变更。
Done Definition: 完成下列验收，记录验证命令、状态及剩余限制。
Outcome: Principal 的空 Roles 与 Claims 保持可用，nil Scopes 形状保留，多次读取独立。

## 证据与影响

第 9 节检查号为 6,27,34,40。证据状态为补证。

已验证非空角色和权限的副本隔离。空与 nil 在 JSON 或授权边界可能有不同形状。 [security/authn/authn.go:69](../../../security/authn/authn.go#L69)

[复审报告](../../../docs/evidence/stable-quality-review-a8c2f28a.md)

## 验收与验证

- [x] 容器形状符合已有契约，编排授权入口不受影响。
- [x] 记录挂载入口、具体断言和结果。已有测试能覆盖时复用并注明。

```sh
go test -race -timeout 60s ./security/authn
```

## 实施证据

- [TestPrincipalCopyPreservesNilAndEmptyContainers](../../../security/authn/authn_test.go#L163)

[批次验证与基准](../review-a8c2f28a/README.md)
