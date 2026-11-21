# Card 1775 — 核对已取消验证与普通无效输入分类

Milestone: 稳定模块质量复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: security
Owned Files: `security/jwt/jwt_verify_test.go`
Depends On: 无

Goal: 失败均拒绝，不回显输入，取消原因按公开约定保留。
Scope: 仅用本机合成取消和普通错误输入验证返回顺序。
Non-goals: 维持稳定公开签名与标准库依赖，将验证限于本卡范围。
Files: `security/jwt/jwt_verify_test.go`。
Tests: 失败均拒绝，不回显输入，取消原因按公开约定保留。
Docs Sync: 复核本卡涉及的已公开约定。
Done Definition: 完成下列验收，记录验证命令、状态及剩余限制。
Outcome: 同一普通无效输入在取消和活跃 Context 下分别返回取消与规范无效错误，均无 claims 或原始值回显。

## 证据与影响

第 9 节检查号为 22,26,40,46。证据状态为补证。

VerifyToken 有入口取消检查和验证路径，要求与应用适配器的错误映射一致。 [security/jwt/verify.go:11](../../../security/jwt/verify.go#L11)

[复审报告](../../../docs/evidence/stable-quality-review-d224acbc.md)

## 验收与验证

- [x] 失败均拒绝，不回显输入，取消原因按公开约定保留。
- [x] 记录运行结果。若已有用例覆盖目标，记录用例及相关断言。

```sh
go test -race -timeout 60s ./security/jwt
```

## 实施证据

- [TestVerifyCanceledContextPrecedesOrdinaryInvalidInput](../../../security/jwt/jwt_verify_test.go#L319)

[批次验证与基准](../review-d224acbc/README.md)
