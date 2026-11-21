# Card 1999 — 核对子指标有 series 计数无名称映射时的合并

Milestone: 稳定模块质量复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: metrics
Owned Files: `metrics/multi_test.go`
Depends On: 无

Goal: 明确聚合公式、子快照不被改写，以及调换顺序后的结果。
Scope: 构造两个本地子收集器，一个只返回显式计数，一个返回正常名称映射。
Non-goals: 维持稳定公开签名与标准库依赖，将验证限于本卡范围。
Files: `metrics/multi_test.go`。
Tests: 明确聚合公式、子快照不被改写，以及调换顺序后的结果。
Docs Sync: 记录核验结论；只在现有契约文字需要澄清时另提文档变更。
Done Definition: 完成下列验收，记录验证命令、状态及剩余限制。
Outcome: 只报告显式活动序列数的子收集器可与普通名称快照合并。交换顺序等价，合并结果独立。

## 证据与影响

第 9 节检查号为 6,31,44,45，当前证据状态为补证。 [质量规则第 9 节](../../../docs/operations/agent-code-quality-rules.md#L267)

聚合同时消费活动序列数和名称快照。子收集器仅报告序列数时需要核对合并结果。 [metrics/collector.go:221](../../../metrics/collector.go#L221)

[复审报告](../../../docs/evidence/stable-quality-review-999d5e4d.md)

## 验收与验证

- [x] 明确聚合公式、子快照不被改写，以及调换顺序后的结果。
- [x] 记录挂载入口、具体断言和结果。已有测试能覆盖时复用并注明。

```sh
go test -race -timeout 60s ./metrics
```

## 实施证据

- [TestMultiCollectorCountsExplicitSeriesWithoutBreakdown](../../../metrics/multi_test.go#L591)
