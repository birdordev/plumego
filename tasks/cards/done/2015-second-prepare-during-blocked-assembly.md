# Card 2015 — 装配中并行 Prepare 与 Server

Milestone: 稳定模块第 9 节复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: core
Owned Files: `core/lifecycle_test.go`
Depends On: 无

Goal: 装配中并行 Prepare 与 Server
Scope: 用通道阻塞首个中间件装配，另一路读取 Server 并尝试 Prepare。
Non-goals: 范围限于本卡涉及的契约，保持公开签名和依赖。
Files: `core/lifecycle_test.go`。
Tests: 释放前 Server 不发布半成品，重叠准备返回错误，结束后单一 server 可用。
Docs Sync: 核对新增验收与已有模块说明一致，记录证据和限制。
Done Definition: 完成以下验收，记录命令、结果与剩余限制。
Outcome: 首个中间件构建阻塞期间，Server 无半成品，重叠 Prepare 返回错误，释放后单一 server 可用。

## 证据与影响

证据状态为补证。第 9 节检查号为 5,28,32,36,47。 [质量规则第 9 节](../../../docs/operations/agent-code-quality-rules.md#L267)

准备状态和 HTTP server 发布分步执行，现有公开入口可同时观察。 [core/http_handler.go:73](../../../core/http_handler.go#L73)

[复审报告](../../../docs/evidence/stable-quality-review-c3a77410.md)

## 验收与验证

- [x] 释放前 Server 不发布半成品，重叠准备返回错误，结束后单一 server 可用。
- [x] 已有测试覆盖相同触发和断言时可直接复用，记录具体用例。

```sh
go test -race -timeout 60s ./core
```

## 实施证据

- [TestShutdownDuringHandlerConstructionAllowsPreparation](../../../core/core_public_test.go#L677)
