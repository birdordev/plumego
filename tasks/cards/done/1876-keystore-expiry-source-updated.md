# Card 1876 — 核对内存密钥容器替换过期条目时的所有权

Milestone: 稳定模块质量复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: security
Owned Files: `security/jwt/keystore_memory_test.go`
Depends On: 无

Goal: 过期键不再枚举，新值带自己的 TTL，旧可变副本不造成写入。
Scope: 采用无凭据的普通字节和合成时间，比较过期前读取、过期、替换与下一次读取。
Non-goals: 维持稳定公开签名与标准库依赖，将验证限于本卡范围。
Files: `security/jwt/keystore_memory_test.go`。
Tests: 过期键不再枚举，新值带自己的 TTL，旧可变副本不造成写入。
Docs Sync: 记录核验结论；只在现有契约文字需要澄清时另提文档变更。
Done Definition: 完成下列验收，记录验证命令、状态及剩余限制。
Outcome: 仅用合成字节验证内存条目过期、同名重写、独立 TTL 和所有权。

## 证据与影响

第 9 节检查号为 26,28,34,40。证据状态为补证。

同键写入保存新字节与 TTL，过期只影响可见性。验证替换后枚举与旧读取副本归属。 [security/jwt/keystore_memory.go:70](../../../security/jwt/keystore_memory.go#L70)

[复审报告](../../../docs/evidence/stable-quality-review-a8c2f28a.md)

## 验收与验证

- [x] 过期键不再枚举，新值带自己的 TTL，旧可变副本不造成写入。
- [x] 记录挂载入口、具体断言和结果。已有测试能覆盖时复用并注明。

```sh
go test -race -timeout 60s ./security/jwt
```

## 实施证据

- [TestExpiredMemoryEntryReplacementOwnsNewTTLAndValue](../../../security/jwt/keystore_memory_test.go#L529)

[批次验证与基准](../review-a8c2f28a/README.md)
