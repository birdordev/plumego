# Card 1951 — 补证 Clear 并行时只发布完整的 Base 统计快照

Milestone: 稳定模块质量复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: metrics
Owned Files: `metrics/concurrency_regression_test.go`
Depends On: 无

Goal: 每份统计内部的总量和名称分布一致，旧快照对后续周期独立。
Scope: 确定一组旧纪录后交叠 Clear、GetStats 和新名称写入，用阶段屏障观测三个允许状态。
Non-goals: 维持稳定公开签名与标准库依赖，将验证限于本卡范围。
Files: `metrics/concurrency_regression_test.go`。
Tests: 每份统计内部的总量和名称分布一致，旧快照对后续周期独立。
Docs Sync: 记录核验结论；只在现有契约文字需要澄清时另提文档变更。
Done Definition: 完成下列验收，记录验证命令、状态及剩余限制。
Outcome: Clear 与新名称写入交叠时仍只能读到一致的统计快照，之前返回的 map 和计数保持独立。

## 证据与影响

第 9 节检查号为 28,32,34,44,47。证据状态为补证。

GetStats 与 Clear 同一 mutex，各自返回独立 map。已有竞争测试，需在新旧两个不同名称的 epoch 观察一致性。 [metrics/collector.go:184](../../../metrics/collector.go#L184)

[复审报告](../../../docs/evidence/stable-quality-review-2b94cf86.md)

## 验收与验证

- [x] 每份统计内部的总量和名称分布一致，旧快照对后续周期独立。
- [x] 记录挂载入口、具体断言和结果。已有测试能覆盖时复用并注明。

```sh
go test -race -timeout 60s ./metrics
```

## 实施证据

- [TestCollectorClearAndNewSeriesDoNotTearSnapshots](../../../metrics/concurrency_regression_test.go#L164)

[批次验证与基准](../review-2b94cf86/README.md)
