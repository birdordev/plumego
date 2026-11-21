# Card 1848 — 聚合未就绪时 Tracker 把未完成组件累计为成功

Milestone: 稳定模块质量复审
Recipe: specs/change-recipes/fix-bug.yaml
Context Package: implementation
Priority: P1
State: done
Primary Module: x/observability/ops
Owned Files: `x/observability/ops/healthhttp/tracker.go`, `x/observability/ops/healthhttp/manager.go`, `x/observability/ops/healthhttp/metrics_test.go`, `docs/modules/x/observability/ops/README.md`
Depends On: 无

Goal: 围绕同一组件验证成功、异常、批次取消和重叠批次。聚合与组件计数一致，普通检查和 Manager 单独调用保留现有行为。
Scope: 在健康宿主内部传递本批组件终态，Tracker 只记录实际执行的结果。保留公开 Manager 接口，为外部实现注明采样限制。
Non-goals: 维持稳定公开签名与标准库依赖，将验证限于本卡范围。
Files: `x/observability/ops/healthhttp/tracker.go`, `x/observability/ops/healthhttp/manager.go`, `x/observability/ops/healthhttp/metrics_test.go`, `docs/modules/x/observability/ops/README.md`。
Tests: 围绕同一组件验证成功、异常、批次取消和重叠批次。聚合与组件计数一致，普通检查和 Manager 单独调用保留现有行为。
Docs Sync: 行为变更后同步清单对应的模块说明并校验生成数据。
Done Definition: 完成下列验收，记录验证命令、状态及剩余限制。
Outcome: 内置 Tracker 直接记录本批实际尝试的终态。中断算失败，取消前未尝试的组件跳过。重叠批次记录各自结果，返回依赖列表也与历史快照隔离。外部 Manager 仍按快照采样。

## 证据与影响

第 9 节检查号为 28,31,32,42,46。证据状态为已复现。

组件先成功一次，下一批在 checker 回调中退出。Manager 返回 unhealthy，Tracker 从上一份健康快照又累计一次成功，成功数为 2，失败数为 0。指标应记录本批终态，跳过未启动的检查。 [x/observability/ops/healthhttp/tracker.go:282](../../../x/observability/ops/healthhttp/tracker.go#L282)

[复审报告](../../../docs/evidence/stable-quality-review-a8c2f28a.md)

## 验收与验证

- [x] 围绕同一组件验证成功、异常、批次取消和重叠批次。聚合与组件计数一致，普通检查和 Manager 单独调用保留现有行为。
- [x] 记录挂载入口、具体断言和结果。已有测试能覆盖时复用并注明。

```sh
go test -race -timeout 60s ./x/observability/ops/healthhttp
make website-sync
make website-check-generated
```

## 实施证据

- [TestAcceptanceTrackerRecordsOnlyBatchAttempts](../../../x/observability/ops/healthhttp/metrics_test.go#L202)
- [TestTrackerOverlappingBatchesRecordTheirOwnComponentOutcome](../../../x/observability/ops/healthhttp/metrics_test.go#L241)

[批次验证与基准](../review-a8c2f28a/README.md)
