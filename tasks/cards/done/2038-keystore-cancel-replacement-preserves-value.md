# Card 2038 — 密钥存储预取消更新保留原快照

Milestone: 稳定模块第 9 节复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: security/jwt
Owned Files: `security/jwt/keystore_memory_test.go`
Depends On: 无

Goal: 密钥存储预取消更新保留原快照
Scope: 先存占位字节，再用预取消上下文替换，同步读取当前测试记录。
Non-goals: 范围限于本卡涉及的契约，保持公开签名和依赖。
Files: `security/jwt/keystore_memory_test.go`。
Tests: 返回 context.Canceled，旧字节仍存在且外部 slice 没有共享。
Docs Sync: 核对新增验收与已有模块说明一致，记录证据和限制。
Done Definition: 完成以下验收，记录命令、结果与剩余限制。
Outcome: 复用普通占位数据的取消更新与输入输出 copy 回归，旧字节在预取消后保持。

## 证据与影响

证据状态为补证。第 9 节检查号为 26,31,34,40。 [质量规则第 9 节](../../../docs/operations/agent-code-quality-rules.md#L267)

存储入口接受 context，并在锁内替换值。需使用普通占位数据验证取消原子性。 [security/jwt/keystore_memory.go:70](../../../security/jwt/keystore_memory.go#L70)

[复审报告](../../../docs/evidence/stable-quality-review-c3a77410.md)

## 验收与验证

- [x] 返回 context.Canceled，旧字节仍存在且外部 slice 没有共享。
- [x] 已有测试覆盖相同触发和断言时可直接复用，记录具体用例。

```sh
go test -race -timeout 60s ./security/jwt
```

## 实施证据

- [TestInMemoryKeyStoreCopiesAndRejectsCanceledOverwrite](../../../security/jwt/keystore_memory_test.go#L395)
