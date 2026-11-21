# Card 2092 — 密钥存储 Keys 快照简单并发

Milestone: 稳定模块第 9 节复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: security/jwt
Owned Files: `security/jwt/keystore_memory_test.go`
Depends On: 无

Goal: 密钥存储 Keys 快照简单并发
Scope: 在可控时钟写两个不同 TTL，占位键跨过期边界并预取消一次读取。
Non-goals: 范围限于本卡涉及的契约，保持公开签名和依赖。
Files: `security/jwt/keystore_memory_test.go`。
Tests: 过期项按现有语义排除，取消分类保留，旧快照不改动下一次输出。
Docs Sync: 核对新增验收与已有模块说明一致，记录证据和限制。
Done Definition: 完成以下验收，记录命令、结果与剩余限制。
Outcome: 占位键列表跨过期边界，预取消返回空结果及 context.Canceled，修改旧快照不影响活键输出。

## 证据与影响

证据状态为补证。第 9 节检查号为 26,32,34,40,47。 [质量规则第 9 节](../../../docs/operations/agent-code-quality-rules.md#L267)

公开存储读取列表受 context 与已过期项影响。使用占位标签比较读取副本。 [security/jwt/keystore_memory.go:94](../../../security/jwt/keystore_memory.go#L94)

[复审报告](../../../docs/evidence/stable-quality-review-2f962d21.md)

## 验收与验证

- [x] 过期项按现有语义排除，取消分类保留，旧快照不改动下一次输出。
- [x] 已有测试覆盖相同触发和断言时可直接复用，记录具体用例。

```sh
go test -race -timeout 60s ./security/jwt
```

## 实施证据

- [TestKeyListSnapshotSurvivesExpiryAndCanceledRead](../../../security/jwt/keystore_memory_test.go#L617)
