# Card 1931 — 核对内存 KeyStore 读锁等待取消后的返回所有权

Milestone: 稳定模块质量复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: security
Owned Files: `security/jwt/keystore_memory_test.go`
Depends On: 无

Goal: 首次无复制返回且错误可分类，后续读取原值保持。
Scope: 用本机内存 KeyStore 和普通 fixture 字节，持写锁触发等待，取消后放行，再读取正常结果。
Non-goals: 维持稳定公开签名与标准库依赖，将验证限于本卡范围。
Files: `security/jwt/keystore_memory_test.go`。
Tests: 首次无复制返回且错误可分类，后续读取原值保持。
Docs Sync: 记录核验结论；只在现有契约文字需要澄清时另提文档变更。
Done Definition: 完成下列验收，记录验证命令、状态及剩余限制。
Outcome: 持有写锁时观察到读入口，取消后释放锁，结果不包含合成字节且仍可分类。后续读取原值。

## 证据与影响

第 9 节检查号为 22,26,34,40。证据状态为补证。

读入口在取锁前后检查 Context。已有写锁取消与枚举测试，需单独核对等待的读取不返回合成字节。 [security/jwt/keystore_memory.go:47](../../../security/jwt/keystore_memory.go#L47)

[复审报告](../../../docs/evidence/stable-quality-review-2b94cf86.md)

## 验收与验证

- [x] 首次无复制返回且错误可分类，后续读取原值保持。
- [x] 记录挂载入口、具体断言和结果。已有测试能覆盖时复用并注明。

```sh
go test -race -timeout 60s ./security/jwt
```

## 实施证据

- [TestKeyStoreReadCanceledAtWaitReturnsNoCopy](../../../security/jwt/keystore_memory_test.go#L570)

[批次验证与基准](../review-2b94cf86/README.md)
