# Card 1803 — 健康聚合并发限制仍为每组件创建 goroutine

Milestone: 稳定模块质量复审
Recipe: specs/change-recipes/fix-bug.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: x/observability
Owned Files: `x/observability/ops/healthhttp/manager.go`, `x/observability/ops/healthhttp/aggregate_regression_test.go`, `docs/modules/x/observability/ops/README.md`
Depends On: 1798

Goal: 以同步屏障核对活动 worker 上界、取消及版本不变量，并记录大组件集的分配。
Scope: 用固定数量 worker 消费组件序列，取消时停止未开始项，明确失败与缺失计数。
Non-goals: 维持稳定公开签名与标准库依赖，将验证限于本卡范围。
Files: `x/observability/ops/healthhttp/manager.go`, `x/observability/ops/healthhttp/aggregate_regression_test.go`, `docs/modules/x/observability/ops/README.md`。
Tests: 以同步屏障核对活动 worker 上界、取消及版本不变量，并记录大组件集的分配。
Docs Sync: 复核模块说明并验证生成数据。
Done Definition: 完成下列验收，记录验证命令、状态及剩余限制。
Outcome: 每批限制活跃检查，固定 worker 消费组件序列，取消后跳过剩余回调。基准记录不同组件数的时延和分配。

## 证据与影响

第 9 节检查号为 26,29,35,42,48。证据状态为源码确认。

每个注册项都创建 worker，信号量只限制进入检查的数量。大量本地配置的组件会产生随注册数增长的等待 goroutine，取消也要等待该批清空。 [x/observability/ops/healthhttp/manager.go:516](../../../x/observability/ops/healthhttp/manager.go#L516)

[复审报告](../../../docs/evidence/stable-quality-review-a074eadb.md)

## 验收与验证

- [x] 以同步屏障核对活动 worker 上界、取消及版本不变量，并记录大组件集的分配。
- [x] 记录运行结果。若已有用例覆盖目标，记录用例及相关断言。

```sh
go test -race -timeout 60s ./x/observability/ops/healthhttp
go test ./x/observability/ops/healthhttp -run '^$' -bench '^BenchmarkAggregateWorkerBound$' -benchmem -benchtime=100ms -count=3
make website-sync
```

## 实施证据

- [TestAcceptanceAggregateWorkerCountAndCancellation](../../../x/observability/ops/healthhttp/aggregate_regression_test.go#L103)
- [BenchmarkAggregateWorkerBound](../../../x/observability/ops/healthhttp/aggregate_regression_test.go#L142)

[批次验证与基准](../review-a074eadb/README.md)
