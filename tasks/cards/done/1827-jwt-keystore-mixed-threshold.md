# Card 1827 — 验证 KeyStore 32 条可见键前后的枚举

Milestone: 稳定模块质量复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: security
Owned Files: `security/jwt/keystore_memory_test.go`
Depends On: 无

Goal: 无丢项或重复，返回 slice 独立，枚举结果仍不含过期项。
Scope: 固定快照时间，验证 31、32、33 条可见集，覆盖接口前后取消。
Non-goals: 维持稳定公开签名与标准库依赖，将验证限于本卡范围。
Files: `security/jwt/keystore_memory_test.go`。
Tests: 无丢项或重复，返回 slice 独立，枚举结果仍不含过期项。
Docs Sync: 复核本卡涉及的已公开约定。
Done Definition: 完成下列验收，记录验证命令、状态及剩余限制。
Outcome: 记录 31、32、33 个有效条目时的基准，核对取消、排除过期条目和快照归属。

## 证据与影响

第 9 节检查号为 26,34,35,40,48。证据状态为补证。

刚切换的小数组策略已有 0、32 和全有效基准。33 条可见键混合大量过期项处在分配切换点。 [security/jwt/keystore_memory.go:105](../../../security/jwt/keystore_memory.go#L105)

[复审报告](../../../docs/evidence/stable-quality-review-a074eadb.md)

## 验收与验证

- [x] 无丢项或重复，返回 slice 独立，枚举结果仍不含过期项。
- [x] 记录运行结果。若已有用例覆盖目标，记录用例及相关断言。

```sh
go test -race -timeout 60s ./security/jwt
go test ./security/jwt -run '^$' -bench '^BenchmarkKeyStoreEnumerationThreshold$' -benchmem -benchtime=100ms -count=3
```

## 实施证据

- [TestKeyStoreEnumerationAcrossSmallBufferBoundary](../../../security/jwt/keystore_memory_test.go#L472)
- [BenchmarkKeyStoreEnumerationThreshold](../../../security/jwt/keystore_memory_test.go#L506)

[批次验证与基准](../review-a074eadb/README.md)
