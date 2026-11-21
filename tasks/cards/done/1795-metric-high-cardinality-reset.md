# Card 1795 — 补证高基数收集清空后的 map 归属与成本

Milestone: 稳定模块质量复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: metrics
Owned Files: `metrics/bench_test.go`, `metrics/collector_test.go`
Depends On: 无

Goal: 系列数准确，早期快照不变，提供分配样本。
Scope: 对照不同有界名称数量，检查 Clear 后名称重复加入和两份快照。
Non-goals: 维持稳定公开签名与标准库依赖，将验证限于本卡范围。
Files: `metrics/bench_test.go`, `metrics/collector_test.go`。
Tests: 系列数准确，早期快照不变，提供分配样本。
Docs Sync: 复核本卡涉及的已公开约定。
Done Definition: 完成下列验收，记录验证命令、状态及剩余限制。
Outcome: 高基数统计清空后重新写入独立系列，前一份快照保存，复用基数基准并记录成本。

## 证据与影响

第 9 节检查号为 34,35,44,48。证据状态为补证。

发散名称进入计数 map；清空应重新配置统计并让旧快照独立。已有输入基准，需记录清空后的成本。 [metrics/collector.go:184](../../../metrics/collector.go#L184)

[复审报告](../../../docs/evidence/stable-quality-review-d224acbc.md)

## 验收与验证

- [x] 系列数准确，早期快照不变，提供分配样本。
- [x] 记录运行结果。若已有用例覆盖目标，记录用例及相关断言。

```sh
go test -race -timeout 60s ./metrics
go test ./metrics -run '^$' -bench '^BenchmarkBaseCollectorNameCardinality$' -benchmem -benchtime=100ms -count=3
```

## 实施证据

- [TestResetAtBoundedCardinalityKeepsPriorSnapshotIndependent](../../../metrics/collector_test.go#L263)
- [BenchmarkBaseCollectorNameCardinality](../../../metrics/bench_test.go#L75)

[批次验证与基准](../review-d224acbc/README.md)
