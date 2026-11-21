# Card 1684 — 验证过滤级别和 discard 模式的 FatalCtx 语义

Milestone: 稳定模块质量复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: log
Owned Files: `log/logger_semantics_test.go`
Depends On: 无

Goal: 验证过滤级别和 discard 模式的 FatalCtx 语义。
Scope: 复用子进程用例核对三个格式、非空 Context 及退出码。
Non-goals: 维持公开签名和依赖；涉及现有兼容契约的调整须先核对调用边界及迁移影响。
Files: `log/logger_semantics_test.go`。
Tests: 退出码始终为 1，按当前格式和级别决定输出。
Docs Sync: 对照原契约核验，若证据要求修订公开行为，先更新实施范围。
Done Definition: 完成测试与验收记录，并标明剩余取证范围。
Outcome: 既有子进程矩阵增加对输出的断言，三个格式、过滤级别和 Context 输入均核对至退出码。

## 证据

检查号为 6,29,43,46，状态为补证。

GoDoc 宣称 Fatal 级别在过滤时同样退出，日志后端有不同实现。 [log/noop.go:28](../../../log/noop.go#L28)

[复审报告](../../../docs/evidence/stable-quality-review-09a7f5d3.md)

## 验收与验证

- [x] 退出码始终为 1，按当前格式和级别决定输出。
- [x] 记录验证命令、状态和剩余范围。若使用既有用例满足验收，记录对应证据。

```sh
go test -race -timeout 60s ./log
```

## 实施证据

[TestLoggerFatalExits](../../../log/logger_semantics_test.go#L198)
