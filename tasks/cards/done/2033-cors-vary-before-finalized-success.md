# Card 2033 — CORS 已含多项 Vary 时合并

Milestone: 稳定模块第 9 节复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: middleware/cors
Owned Files: `middleware/cors/cors_test.go`
Depends On: 无

Goal: CORS 已含多项 Vary 时合并
Scope: 在进入 CORS 前放入两个已有 Vary 字段，运行允许的实际请求。
Non-goals: 范围限于本卡涉及的契约，保持公开签名和依赖。
Files: `middleware/cors/cors_test.go`。
Tests: 保留既有 token，Origin 出现一次，next 执行一次。
Docs Sync: 核对新增验收与已有模块说明一致，记录证据和限制。
Done Definition: 完成以下验收，记录命令、结果与剩余限制。
Outcome: 既有多行 Vary 保留两个通用 token，Origin 仅出现一次，每次请求只进入一次 next。

## 证据与影响

证据状态为补证。第 9 节检查号为 20,23,34,39。 [质量规则第 9 节](../../../docs/operations/agent-code-quality-rules.md#L267)

使用明确来源时 CORS 在现有响应头上工作，需锁定对已有通用 Vary 的兼容。 [middleware/cors/cors.go:151](../../../middleware/cors/cors.go#L151)

[复审报告](../../../docs/evidence/stable-quality-review-c3a77410.md)

## 验收与验证

- [x] 保留既有 token，Origin 出现一次，next 执行一次。
- [x] 已有测试覆盖相同触发和断言时可直接复用，记录具体用例。

```sh
go test -race -timeout 60s ./middleware/cors
```

## 实施证据

- [TestAllowedOriginMergesExistingVaryFields](../../../middleware/cors/cors_test.go#L737)
