# Card 1978 — 核对零 TTL 重写会清除旧过期时间

Milestone: 稳定模块质量复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: security/jwt
Owned Files: `security/jwt/keystore_memory_test.go`
Depends On: 无

Goal: 新值仍可读取且只列一次，测试只使用本机合成数据。
Scope: 用合成值配合可控时间替换旧记录，跨越旧截止后观察 Get 和 Keys。
Non-goals: 维持稳定公开签名与标准库依赖，将验证限于本卡范围。
Files: `security/jwt/keystore_memory_test.go`。
Tests: 新值仍可读取且只列一次，测试只使用本机合成数据。
Docs Sync: 记录核验结论；只在现有契约文字需要澄清时另提文档变更。
Done Definition: 完成下列验收，记录验证命令、状态及剩余限制。
Outcome: 合成条目以零 TTL 重写后跨越旧截止仍可读取，Keys 仅列一次。

## 证据与影响

第 9 节检查号为 31,34,40,47，当前证据状态为补证。 [质量规则第 9 节](../../../docs/operations/agent-code-quality-rules.md#L267)

同一个普通名字可以覆盖旧条目，新 TTL 决定当前状态。零 TTL 覆盖临时条目应单独验证。 [security/jwt/keystore_memory.go:70](../../../security/jwt/keystore_memory.go#L70)

[复审报告](../../../docs/evidence/stable-quality-review-999d5e4d.md)

## 验收与验证

- [x] 新值仍可读取且只列一次，测试只使用本机合成数据。
- [x] 记录挂载入口、具体断言和结果。已有测试能覆盖时复用并注明。

```sh
go test -race -timeout 60s ./security/jwt
```

## 实施证据

- [TestKeystoreZeroTTLReplacementOutlivesPreviousDeadline](../../../security/jwt/keystore_memory_test.go#L596)
