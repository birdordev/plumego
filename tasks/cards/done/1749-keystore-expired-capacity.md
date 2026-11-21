# Card 1749 — 内存 JWT KeyStore 遍历并分配过期项容量

Milestone: 稳定模块质量复审
Recipe: specs/change-recipes/fix-bug.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: security
Owned Files: `security/jwt/keystore_memory.go`, `security/jwt/keystore_memory_test.go`
Depends On: 无

Goal: 核对可见集合、取消语义与高过期比例下的 B/op。
Scope: 先用固定时间和过期比例测量，考虑缩小初始容量，保持过期和未知的分类。
Non-goals: 维持稳定公开签名与标准库依赖，将验证限于本卡范围。
Files: `security/jwt/keystore_memory.go`, `security/jwt/keystore_memory_test.go`。
Tests: 核对可见集合、取消语义与高过期比例下的 B/op。
Docs Sync: 复核本卡涉及的已公开约定。
Done Definition: 完成下列验收，记录验证命令、状态及剩余限制。
Outcome: 小可见集合使用局部数组，超过阈值后一次分配，扣除已经确认过期的条目。保留空切片、取消、过期和未知错误语义。测量详见批次前后对照。

## 证据与影响

第 9 节检查号为 14,29,35,40,48。证据状态为源码确认。

已文档化的过期条目为保留分类而留存。KeysContext 按全量条目预分配，即使可见集合为空；重复读取会承担已过期历史的分配成本。 [security/jwt/keystore_memory.go:105](../../../security/jwt/keystore_memory.go#L105)

[复审报告](../../../docs/evidence/stable-quality-review-d224acbc.md)

## 验收与验证

- [x] 核对可见集合、取消语义与高过期比例下的 B/op。
- [x] 记录运行结果。若已有用例覆盖目标，记录用例及相关断言。

```sh
go test -race -timeout 60s ./security/jwt
go test ./security/jwt -run '^$' -bench '^BenchmarkKeyStoreKeysExpiryRatio$' -benchmem -benchtime=100ms -count=3
```

## 实施证据

- [BenchmarkKeyStoreKeysExpiryRatio](../../../security/jwt/keystore_memory_test.go#L418)
- [TestExpiredKeySnapshotAndOverwriteUseNewValue](../../../security/jwt/keystore_memory_test.go#L442)
- [TestInMemoryKeyStoreCanceledWhileWaitingToRead](../../../security/jwt/keystore_memory_test.go#L38)

[批次验证与基准](../review-d224acbc/README.md)
