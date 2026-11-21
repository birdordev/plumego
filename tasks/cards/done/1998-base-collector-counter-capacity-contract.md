# Card 1998 — 核对基础指标在合成计数上限的行为

Milestone: 稳定模块质量复审
Recipe: specs/change-recipes/analysis-only.yaml
Context Package: review
Priority: P3
State: done
Primary Module: metrics
Owned Files: `metrics/collector.go`, `metrics/stats_contract_test.go`
Depends On: 无

Goal: 记录溢出观察和兼容约束，明确是否需要后续修复。
Scope: 用同包合成计数器移至边界，比对基础与合并层输出，并核对现有统计契约。
Non-goals: 维持稳定公开签名与标准库依赖，将验证限于本卡范围。
Files: `metrics/collector.go`, `metrics/stats_contract_test.go`。
Tests: 记录溢出观察和兼容约束，明确是否需要后续修复。
Docs Sync: 记录核验结论；只在现有契约文字需要澄清时另提文档变更。
Done Definition: 完成下列验收，记录验证命令、状态及剩余限制。
Outcome: 合成初值在 MaxInt64 后记录一条，基础和聚合输出都可见 MinInt64。当前无真实负载可达证据。后续加固应先统一基础记录计数的饱和规则，并核对公开计量语义。

## 证据与影响

第 9 节检查号为 6,34,44,46，当前证据状态为补证。 [质量规则第 9 节](../../../docs/operations/agent-code-quality-rules.md#L267)

基础计数直接递增，合并层已有饱和加法。当前只观察到两层写法不同，尚无实际工作负载触及上限的证据。 [metrics/collector.go:143](../../../metrics/collector.go#L143)

[复审报告](../../../docs/evidence/stable-quality-review-999d5e4d.md)

## 验收与验证

- [x] 记录溢出观察和兼容约束，明确是否需要后续修复。
- [x] 记录挂载入口、具体断言和结果。已有测试能覆盖时复用并注明。

## 实施证据

把同包收集器的总数、错误数与给定名称的计数设为 MaxInt64，然后通过 Record 写入一条有正常合成错误的记录。对比基础直接读取和合并读取，三组计数均为 MinInt64。 探针通过，临时 overlay 位于工作区外。

```sh
go test -overlay=/tmp/999d-analysis-overlay.json -race -v -timeout 45s ./metrics -run '^TestReviewBaseCollectorCounterBoundary$'
```
