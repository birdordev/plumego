# Card 2126 — 明确 App 与组注册辅助函数的作用域

Milestone: 稳定模块第 9 节复审
Recipe: specs/change-recipes/fix-bug.yaml
Context Package: implementation
Priority: P3
State: done
Primary Module: core
Owned Files: `core/routing.go`
Depends On: 无

Goal: 明确 App 与组注册辅助函数的作用域。
Scope: 收窄注释术语，说明 App 入口与 RouteGroup 各自入口以及共同委托的 Router.AddRoute。
Non-goals: 保持公开签名、依赖和其余模块的行为。
Files: `core/routing.go`
Tests: 复用已有根与组注册错误分类、冻结优先级测试。
Docs Sync: 同步私有 helper 注释。
Done Definition: 完成以下回归和验收，记录实际命令及结果。
Outcome: 私有 App 注册入口与 RouteGroup 入口的范围在注释中明确，二者继续委托 Router.AddRoute。core 注册契约测试通过。

## 证据与影响

证据状态为源码确认。第 9 节检查号为 9,12,16,19,52。App 私有 helper 的注释声称覆盖全部注册。组入口实际走独立 helper，跟踪冻结和错误分类时容易漏读组分支。 [core/routing.go:12](../../../core/routing.go#L12) [core/routing.go:150](../../../core/routing.go#L150) [core/routing_test.go:625](../../../core/routing_test.go#L625)

[复审报告](../../../docs/evidence/stable-quality-review-6e8d53f9.md)

## 验收与验证

- [x] 复用已有根与组注册错误分类、冻结优先级测试。
- [x] 完成以下回归和验收，记录实际命令及结果。

```sh
go test -race -timeout 60s ./core
```

实现时先运行 `make fmt`，按实际改动范围预览并运行 diff 门禁。
