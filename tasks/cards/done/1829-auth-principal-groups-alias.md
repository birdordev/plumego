# Card 1829 — 核对认证 Principal 组成数据的所有权

Milestone: 稳定模块质量复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: security
Owned Files: `security/authn/authn_test.go`
Depends On: 无

Goal: 调用方明白可修改范围，撤销另有独立策略。
Scope: 用合成普通主体对照原对象和 Context Accessor 返回。
Non-goals: 维持稳定公开签名与标准库依赖，将验证限于本卡范围。
Files: `security/authn/authn_test.go`。
Tests: 调用方明白可修改范围，撤销另有独立策略。
Docs Sync: 复核本卡涉及的已公开约定。
Done Definition: 完成下列验收，记录验证命令、状态及剩余限制。
Outcome: 复用已有 Principal 写入、读取和快照可变字段所有权回归。

## 证据与影响

第 9 节检查号为 5,27,34,40。证据状态为补证。

从授权链跨 Context 取得 Principal。依赖策略消费时，角色和权限切片的可变边界需验证。 [security/authn/authn.go:48](../../../security/authn/authn.go#L48)

[复审报告](../../../docs/evidence/stable-quality-review-a074eadb.md)

## 验收与验证

- [x] 调用方明白可修改范围，撤销另有独立策略。
- [x] 记录运行结果。若已有用例覆盖目标，记录用例及相关断言。

```sh
go test -race -timeout 60s ./security/authn
```

## 实施证据

- [TestPrincipalContextCopiesMutableFields](../../../security/authn/authn_test.go#L29)

[批次验证与基准](../review-a074eadb/README.md)
