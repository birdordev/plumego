# Card 1778 — 验证 KeyStore 过期读取和覆盖交错

Milestone: 稳定模块质量复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: security
Owned Files: `security/jwt/keystore_memory_test.go`
Depends On: 无

Goal: 按每次快照的时间归类，返回值与存储内存隔离。
Scope: 用普通短 TTL 和受控顺序重复读取、覆盖、重新枚举。
Non-goals: 维持稳定公开签名与标准库依赖，将验证限于本卡范围。
Files: `security/jwt/keystore_memory_test.go`。
Tests: 按每次快照的时间归类，返回值与存储内存隔离。
Docs Sync: 复核本卡涉及的已公开约定。
Done Definition: 完成下列验收，记录验证命令、状态及剩余限制。
Outcome: 合成时钟验证过期快照。覆盖后立即可见，新值不受旧读取副本变化影响。

## 证据与影响

第 9 节检查号为 26,31,32,40。证据状态为补证。

Get 在锁内取得值后判时间；覆盖与读取各可有不同观察时刻。 [security/jwt/keystore_memory.go:61](../../../security/jwt/keystore_memory.go#L61)

[复审报告](../../../docs/evidence/stable-quality-review-d224acbc.md)

## 验收与验证

- [x] 按每次快照的时间归类，返回值与存储内存隔离。
- [x] 记录运行结果。若已有用例覆盖目标，记录用例及相关断言。

```sh
go test -race -timeout 60s ./security/jwt
```

## 实施证据

- [TestExpiredKeySnapshotAndOverwriteUseNewValue](../../../security/jwt/keystore_memory_test.go#L442)

[批次验证与基准](../review-d224acbc/README.md)
