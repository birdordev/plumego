# Card 1644 — 日志构造器只使用首个 variadic 配置

Milestone: 稳定模块质量复审
Recipe: specs/change-recipes/fix-bug.yaml
Context Package: implementation
Priority: P3
State: done
Primary Module: log
Owned Files: `log/logger.go`, `log/json_test.go`, `docs/modules/log/README.md`
Depends On: 无

Goal: 日志构造器只使用首个 variadic 配置。
Scope: 明确使用第一个配置的兼容语义，并记录推荐单配置用法。
Non-goals: 维持公开签名和依赖；涉及现有兼容契约的调整须先核对调用边界及迁移影响。
Files: `log/logger.go`, `log/json_test.go`, `docs/modules/log/README.md`。
Tests: 锁定两个配置冲突时所选格式和目的地。
Docs Sync: 对照原契约核验，若证据要求修订公开行为，先更新实施范围。
Done Definition: 完成测试与验收记录，并标明剩余取证范围。
Outcome: GoDoc 明确只用第一个配置。冲突格式和目的地由定向回归确认。

## 证据

检查号为 6,14,16，状态为源码确认。

签名允许传多个配置，GoDoc 只解释无配置和格式选项。实现静默忽略后续配置。 [log/logger.go:95](../../../log/logger.go#L95)

[复审报告](../../../docs/evidence/stable-quality-review-09a7f5d3.md)

## 验收与验证

- [x] 锁定两个配置冲突时所选格式和目的地。
- [x] 记录验证命令、状态和剩余范围。若使用既有用例满足验收，记录对应证据。

```sh
go test -race -timeout 60s ./log
```

## 实施证据

[TestNewLoggerUsesFirstConfig](../../../log/json_test.go#L849)
