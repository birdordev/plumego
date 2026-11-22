# Card 2123 — 在生命周期准入中避免锁内调用上下文实现

Milestone: 稳定模块第 9 节复审
Recipe: specs/change-recipes/fix-bug.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: core
Owned Files: `core/lifecycle_group.go`, `core/lifecycle_group_test.go`
Depends On: 无

Goal: 在生命周期准入中避免锁内调用上下文实现。
Scope: 锁外读取取消信号，临界区只检查信号和 phase。取消时保持 idle，解锁后返回错误，维持已经启动的错误优先级。
Non-goals: 保持公开签名、依赖和其余模块的行为。
Files: `core/lifecycle_group.go`, `core/lifecycle_group_test.go`
Tests: 以自定义上下文和通道确认检查发生于锁外。断言预取消后仍能 Add 并启动，重叠 Start 仍按原契约返回。
Docs Sync: 核对 Start 的 GoDoc 和生命周期阶段说明。
Done Definition: 完成以下回归和验收，记录实际命令及结果。
Outcome: Start 准入在锁内查看已取得的取消信号，取消错误在解锁后返回。反向校正原来要求锁内取样的断言。预取消保持 idle，随后注册、启动和停止均通过。

## 证据与影响

证据状态为源码确认。第 9 节检查号为 26,28,32,33,36,47。Start 在 g.mu 临界区调用外部 Context.Err。同步检查组状态的自定义上下文可重入同一把锁，回调本身的非阻塞重入保证覆盖不到准入阶段。 [core/lifecycle_group.go:134](../../../core/lifecycle_group.go#L134) [core/lifecycle_group.go:153](../../../core/lifecycle_group.go#L153) [core/lifecycle_group_test.go:739](../../../core/lifecycle_group_test.go#L739)

[复审报告](../../../docs/evidence/stable-quality-review-6e8d53f9.md)

## 验收与验证

- [x] 以自定义上下文和通道确认检查发生于锁外。断言预取消后仍能 Add 并启动，重叠 Start 仍按原契约返回。
- [x] 完成以下回归和验收，记录实际命令及结果。

```sh
go test -race -timeout 60s ./core
```

实现时先运行 `make fmt`，按实际改动范围预览并运行 diff 门禁。
