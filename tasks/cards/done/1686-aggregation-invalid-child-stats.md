# Card 1686 — 核对聚合器处理失约子统计的语义

Milestone: 稳定模块质量复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: metrics
Owned Files: `metrics/collector.go`, `metrics/multi_test.go`
Depends On: 无

Goal: 核对聚合器处理失约子统计的语义。
Scope: 用人工子快照对照有效上限与负值输入，明确调用者契约后决定是否防御。
Non-goals: 维持公开签名和依赖；涉及现有兼容契约的调整须先核对调用边界及迁移影响。
Files: `metrics/collector.go`, `metrics/multi_test.go`。
Tests: 正常统计和空子快照保持；不把非法输入的未决行为当作已知缺陷。
Docs Sync: 对照原契约核验，若证据要求修订公开行为，先更新实施范围。
Done Definition: 完成测试与验收记录，并标明剩余取证范围。
Outcome: GoDoc 明确子统计必须报告非负计数。保留原聚合策略，复用有效上限、空值和 active-series 回退的回归。

## 证据

检查号为 14,22,32,44，状态为补证。

聚合饱和处理正向值，子接口如返回负计数则未定义。 [metrics/multi.go:112](../../../metrics/multi.go#L112)

[复审报告](../../../docs/evidence/stable-quality-review-09a7f5d3.md)

## 验收与验证

- [x] 正常统计和空子快照保持；不把非法输入的未决行为当作已知缺陷。
- [x] 记录验证命令、状态和剩余范围。若使用既有用例满足验收，记录对应证据。

```sh
go test -race -timeout 60s ./metrics
```

## 实施证据

[TestMultiCollectorGetStatsSaturatesCounters](../../../metrics/multi_test.go#L152) · [TestMultiCollectorGetStatsNormalizesChildActiveSeries](../../../metrics/multi_test.go#L193) · [TestMultiCollectorEmpty](../../../metrics/multi_test.go#L289)
