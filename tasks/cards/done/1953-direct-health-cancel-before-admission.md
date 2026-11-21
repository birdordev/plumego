# Card 1953 — 未执行的单组件检查发布失败并挤掉有效检查

Milestone: 稳定模块质量复审
Recipe: specs/change-recipes/fix-bug.yaml
Context Package: implementation
Priority: P1
State: done
Primary Module: x/observability/ops
Owned Files: `x/observability/ops/healthhttp/manager.go`, `x/observability/ops/healthhttp/aggregate_regression_test.go`, `x/observability/ops/healthhttp/component_regression_test.go`, `docs/modules/x/observability/ops/README.md`
Depends On: 无

Goal: 串行和交错情况下，预取消均返回 context.Canceled 且不修改快照或抢占版本。真实失败仍发布，批次路径和手工就绪回归通过。
Scope: 在抢占版本前处理已取消上下文，并让零次尝试不发布健康状态。保留关闭、未注册和已实际执行检查的返回语义。
Non-goals: 维持稳定公开签名与标准库依赖，将验证限于本卡范围。
Files: `x/observability/ops/healthhttp/manager.go`, `x/observability/ops/healthhttp/aggregate_regression_test.go`, `x/observability/ops/healthhttp/component_regression_test.go`, `docs/modules/x/observability/ops/README.md`。
Tests: 串行和交错情况下，预取消均返回 context.Canceled 且不修改快照或抢占版本。真实失败仍发布，批次路径和手工就绪回归通过。
Docs Sync: 行为变更后同步清单对应的模块说明并校验生成数据。
Done Definition: 完成下列验收，记录验证命令、状态及剩余限制。
Outcome: 首次检查的准入与版本登记合并。预取消返回上下文错误，快照和活动检查资格保持，HTTP 请求仍返回 503。

## 证据与影响

第 9 节检查号为 6,22,26,31,42,46,47，当前证据状态为已复现。 [质量规则第 9 节](../../../docs/operations/agent-code-quality-rules.md#L267)

已有健康结果时让一次有效检查在回调内等待，再发起预取消的单组件检查。后者未执行回调，仍发布 unhealthy 和零次尝试，有效检查完成后收到 superseded。 [x/observability/ops/healthhttp/manager.go:266](../../../x/observability/ops/healthhttp/manager.go#L266)

[复审报告](../../../docs/evidence/stable-quality-review-999d5e4d.md)

## 验收与验证

- [x] 串行和交错情况下，预取消均返回 context.Canceled 且不修改快照或抢占版本。真实失败仍发布，批次路径和手工就绪回归通过。
- [x] 记录挂载入口、具体断言和结果。已有测试能覆盖时复用并注明。

```sh
go test -race -timeout 60s ./x/observability/ops/healthhttp
make website-sync
make website-check-generated
```

## 实施证据

- [TestPreCanceledDirectCheckPreservesActivePublication](../../../x/observability/ops/healthhttp/aggregate_regression_test.go#L278)
- [TestComponentHealthCanceledProbeFailsClosed](../../../x/observability/ops/healthhttp/component_regression_test.go#L95)
- [TestAcceptanceRetryDelayReturnsContextCauseAndCountsAttempts](../../../x/observability/ops/healthhttp/component_regression_test.go#L126)

所属回归在 999d5e4d 的生产代码 overlay 上如预期失败，修改后连续十次竞态运行通过。
