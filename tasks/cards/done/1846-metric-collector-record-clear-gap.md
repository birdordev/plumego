# Card 1846 — 测量读多写少时高基数统计快照分配

Milestone: 稳定模块质量复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: metrics
Owned Files: `metrics/bench_test.go`
Depends On: 无

Goal: 核对 map 归属、结果数量与每次分配。
Scope: 准备固定系列数后分开测连续 GetStats、空快照及清空后重建。
Non-goals: 维持稳定公开签名与标准库依赖，将验证限于本卡范围。
Files: `metrics/bench_test.go`。
Tests: 核对 map 归属、结果数量与每次分配。
Docs Sync: 复核本卡涉及的已公开约定。
Done Definition: 完成下列验收，记录验证命令、状态及剩余限制。
Outcome: 记录空、少量和高基数快照读取分配，验证清空后旧快照的独立容量。 另以既有基准核对建表、读取和清空的综合成本。

## 证据与影响

第 9 节检查号为 32,35,44,48。证据状态为补证。

旧基准组合建表、读取和清空，无法单独回答读取快照成本。 [metrics/collector.go:169](../../../metrics/collector.go#L169)

[复审报告](../../../docs/evidence/stable-quality-review-a074eadb.md)

## 验收与验证

- [x] 核对 map 归属、结果数量与每次分配。
- [x] 记录运行结果。若已有用例覆盖目标，记录用例及相关断言。

```sh
go test -race -timeout 60s ./metrics
go test ./metrics -run '^$' -bench '^BenchmarkCollectorStatsSnapshot$' -benchmem -benchtime=100ms -count=3
```

## 实施证据

- [BenchmarkCollectorStatsSnapshot](../../../metrics/bench_test.go#L124)
- [TestResetAtBoundedCardinalityKeepsPriorSnapshotIndependent](../../../metrics/collector_test.go#L263)
- [BenchmarkBaseCollectorNameCardinality](../../../metrics/bench_test.go#L75)

[批次验证与基准](../review-a074eadb/README.md)
