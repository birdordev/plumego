# Card 1980 — 核对 Principal 的 nil 与空属性快照

Milestone: 稳定模块质量复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P3
State: done
Primary Module: security/authn
Owned Files: `security/authn/authn_test.go`
Depends On: 无

Goal: 读取保留约定形状，修改取出副本不影响其他调用。
Scope: 构造不含凭据的 Principal，比较零值属性与显式空集合的往返。
Non-goals: 维持稳定公开签名与标准库依赖，将验证限于本卡范围。
Files: `security/authn/authn_test.go`。
Tests: 读取保留约定形状，修改取出副本不影响其他调用。
Docs Sync: 记录核验结论；只在现有契约文字需要澄清时另提文档变更。
Done Definition: 完成下列验收，记录验证命令、状态及剩余限制。
Outcome: 公开上下文入口保留 nil 和显式空属性的区别，取出副本的改动不渗入其他读取。

## 证据与影响

第 9 节检查号为 25,31,40,45，当前证据状态为补证。 [质量规则第 9 节](../../../docs/operations/agent-code-quality-rules.md#L267)

Principal 的存入和取出都会复制属性。nil 和空属性集合的公开观察需区分。 [security/authn/authn.go:69](../../../security/authn/authn.go#L69)

[复审报告](../../../docs/evidence/stable-quality-review-999d5e4d.md)

## 验收与验证

- [x] 读取保留约定形状，修改取出副本不影响其他调用。
- [x] 记录挂载入口、具体断言和结果。已有测试能覆盖时复用并注明。

```sh
go test -race -timeout 60s ./security/authn
```

## 实施证据

- [TestPrincipalSnapshotDistinguishesNilAndEmptyAttributes](../../../security/authn/authn_test.go#L177)
