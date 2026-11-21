# Card 1628 — 量化并验证 HTTP 两入口统计一致性

Milestone: 本轮质量审查
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: metrics
Owned Files: `metrics/concurrency_regression_test.go`, `metrics/bench_test.go`
Depends On: 无

Goal: 量化并验证 HTTP 两入口统计一致性。
Scope: 在固定状态表上混跑两个入口并校验总量，保留基准。
Non-goals: 保留现有公开签名、依赖与模块边界。测试若证实新的缺陷，先记录证据再单独安排修复。
Files: `metrics/concurrency_regression_test.go`, `metrics/bench_test.go`。
Tests: 总数、错误数、名称计数精确一致；无状态及无法解析的标签有明确约定。
Docs Sync: 本卡按现有契约验证。若取证表明契约需调整，先更新后续卡范围。
Done Definition: 完成下列验收和验证，记录证据与剩余风险。
Outcome: 混跑两个 HTTP 入口断言总量及错误数，复用无效标签测试和两入口基准。

## 证据与影响

- 检查号：6,32,44,45。证据状态：补证。
- Record 从 status 标签分类，ObserveHTTP 直接读状态码。 [metrics/collector.go:226](../../../metrics/collector.go#L226)
- [审查报告](../../../docs/evidence/stable-quality-review-dee7aa0f.md)

## 验收与验证

- [x] 总数、错误数、名称计数精确一致；无状态及无法解析的标签有明确约定。
- [x] 运行以下命令并记录结果、失败路径和未验证范围。修改 Go 文件前完成仓库预检，验证前先执行仓库格式化。

```sh
go test -race -timeout 60s ./metrics
```

## 实施证据

[TestConcurrentHTTPRecordAndObserveClassification](../../../metrics/concurrency_regression_test.go#L144) · [TestBaseMetricsCollectorIgnoresInvalidHTTPStatusForErrorClassification](../../../metrics/collector_test.go#L147) · [BenchmarkBaseCollectorObserveHTTP](../../../metrics/bench_test.go#L43) · [BenchmarkBaseCollectorRecord](../../../metrics/bench_test.go#L25)
