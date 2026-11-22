# Card 2120 — 阻止旧批次失败覆盖替换组件的就绪状态

Milestone: 稳定模块第 9 节复审
Recipe: specs/change-recipes/fix-bug.yaml
Context Package: implementation
Priority: P1
State: done
Primary Module: x/observability/ops
Owned Files: `x/observability/ops/healthhttp/manager.go`, `x/observability/ops/healthhttp/aggregate_regression_test.go`
Depends On: 无

Goal: 阻止旧批次失败覆盖替换组件的就绪状态。
Scope: 在批次发布临界区校验注册身份与检查版本，丢弃过期的发布资格，独立保留真实检查尝试的历史和计量。覆盖注销、同代真实失败以及已关闭的终态。
Non-goals: 保持公开签名、依赖和其余模块的行为。
Files: `x/observability/ops/healthhttp/manager.go`, `x/observability/ops/healthhttp/aggregate_regression_test.go`
Tests: 用通道卡住旧检查，替换并验证新组件后释放旧失败，断言旧批次不改写较新的就绪状态。另核对关闭和手动标记仍优先。
Docs Sync: 核对健康宿主说明中的版本与历史口径。实现后仅同步受影响的描述。
Done Definition: 完成以下回归和验收，记录实际命令及结果。
Outcome: 首个检查保存注册身份和检查版本。批次提交时会排除失去归属的失败，同时保留实际尝试的计量。通道驱动的替换回归由 Ready=true 开始，旧失败完成后仍为 true；关闭和手动优先级回归通过。健康模块说明已同步。

## 证据与影响

证据状态为已复现。第 9 节检查号为 26,28,31,32,42,45,47。旧批次阻塞期间，同名组件被替换并通过单项检查。旧结果不能覆盖组件状态，但仍进入全局失败列表，导致 Ready 从 true 变为 false，同时 Components 中该项为 true。 [x/observability/ops/healthhttp/manager.go:318](../../../x/observability/ops/healthhttp/manager.go#L318) [x/observability/ops/healthhttp/manager.go:371](../../../x/observability/ops/healthhttp/manager.go#L371) [x/observability/ops/healthhttp/manager.go:395](../../../x/observability/ops/healthhttp/manager.go#L395) [x/observability/ops/healthhttp/aggregate_regression_test.go:169](../../../x/observability/ops/healthhttp/aggregate_regression_test.go#L169)

[复审报告](../../../docs/evidence/stable-quality-review-6e8d53f9.md)

## 验收与验证

- [x] 用通道卡住旧检查，替换并验证新组件后释放旧失败，断言旧批次不改写较新的就绪状态。另核对关闭和手动标记仍优先。
- [x] 完成以下回归和验收，记录实际命令及结果。

```sh
go test -race -timeout 60s ./x/observability/ops/healthhttp
```

实现时先运行 `make fmt`，按实际改动范围预览并运行 diff 门禁。
