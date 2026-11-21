# Card 1605 — 补证 JWT 内存存储对输入输出的所有权

Milestone: 本轮质量审查
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: security
Owned Files: `security/jwt/keystore_memory_test.go`
Depends On: 无

Goal: 补证 JWT 内存存储对输入输出的所有权。
Scope: 用普通测试字节串补所有权矩阵。
Non-goals: 保留现有公开签名、依赖与模块边界。测试若证实新的缺陷，先记录证据再单独安排修复。
Files: `security/jwt/keystore_memory_test.go`。
Tests: 修改输入、读取返回值均不会改变已存副本；取消的覆盖写不生效。
Docs Sync: 本卡按现有契约验证。若取证表明契约需调整，先更新后续卡范围。
Done Definition: 完成下列验收和验证，记录证据与剩余风险。
Outcome: 测试字节双向复制与取消覆盖写，不影响已提交值。

## 证据与影响

- 检查号：26,34,40,46。证据状态：补证。
- 存储负责 TTL 和字节副本，需核对取消、读写和调用者变更交叠。 [security/jwt/keystore_memory.go:70](../../../security/jwt/keystore_memory.go#L70)
- [审查报告](../../../docs/evidence/stable-quality-review-dee7aa0f.md)

## 验收与验证

- [x] 修改输入、读取返回值均不会改变已存副本；取消的覆盖写不生效。
- [x] 运行以下命令并记录结果、失败路径和未验证范围。修改 Go 文件前完成仓库预检，验证前先执行仓库格式化。

```sh
go test -race -timeout 60s ./security/jwt
```

## 实施证据

[TestInMemoryKeyStoreCopiesAndRejectsCanceledOverwrite](../../../security/jwt/keystore_memory_test.go#L395) · [TestInMemoryKeyStoreCanceledWhileWaitingToSet](../../../security/jwt/keystore_memory_test.go#L18)
