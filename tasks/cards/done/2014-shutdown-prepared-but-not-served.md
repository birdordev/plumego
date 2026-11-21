# Card 2014 — 未 Serve 前关闭再尝试启动

Milestone: 稳定模块第 9 节复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: core
Owned Files: `core/lifecycle_test.go`
Depends On: 无

Goal: 未 Serve 前关闭再尝试启动
Scope: 用显式 listener 调用 Server.Serve，再检查停服错误与直接 ServeHTTP。
Non-goals: 范围限于本卡涉及的契约，保持公开签名和依赖。
Files: `core/lifecycle_test.go`。
Tests: 空闲关闭后重复关闭，Serve 返回 ErrServerClosed，原应用仍分派已注册处理器。
Docs Sync: 核对新增验收与已有模块说明一致，记录证据和限制。
Done Definition: 完成以下验收，记录命令、结果与剩余限制。
Outcome: 准备后空闲关闭两次。后续 Server.Serve 返回 ErrServerClosed，直接分派仍返回已注册的状态。

## 证据与影响

证据状态为补证。第 9 节检查号为 28,31,36,46。 [质量规则第 9 节](../../../docs/operations/agent-code-quality-rules.md#L267)

Shutdown 在准备完成后可读到 server，随后 stdlib 管理终态。考察首次绑定前的关闭和直接分派是否共同符合说明。 [core/lifecycle.go:74](../../../core/lifecycle.go#L74)

[复审报告](../../../docs/evidence/stable-quality-review-c3a77410.md)

## 验收与验证

- [x] 空闲关闭后重复关闭，Serve 返回 ErrServerClosed，原应用仍分派已注册处理器。
- [x] 已有测试覆盖相同触发和断言时可直接复用，记录具体用例。

```sh
go test -race -timeout 60s ./core
```

## 实施证据

- [TestShutdownBeforeServePreventsStartAndKeepsDirectHandler](../../../core/lifecycle_test.go#L1534)
