# Card 1895 — 核对空名称记录的总量与系列计数

Milestone: 稳定模块质量复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: metrics
Owned Files: `metrics/collector_test.go`
Depends On: 无

Goal: 全局计数、系列和 reset 时间严格符合现有承诺，空名不会隐式产生系列。
Scope: 对同一 base collector 连续记录空名普通、空名带错误、公开 HTTP 记录，最后 Clear。
Non-goals: 维持稳定公开签名与标准库依赖，将验证限于本卡范围。
Files: `metrics/collector_test.go`。
Tests: 全局计数、系列和 reset 时间严格符合现有承诺，空名不会隐式产生系列。
Docs Sync: 记录核验结论；只在现有契约文字需要澄清时另提文档变更。
Done Definition: 完成下列验收，记录验证命令、状态及剩余限制。
Outcome: 空名称记录只增加总量及相应错误数，不形成系列。

## 证据与影响

第 9 节检查号为 6,14,32,44。证据状态为补证。

总量和错误量会增长，只有非空名称进入 NameBreakdown。需联测空名错误和接着处理 HTTP 记录后的统计。 [metrics/collector.go:149](../../../metrics/collector.go#L149)

[复审报告](../../../docs/evidence/stable-quality-review-a8c2f28a.md)

## 验收与验证

- [x] 全局计数、系列和 reset 时间严格符合现有承诺，空名不会隐式产生系列。
- [x] 记录挂载入口、具体断言和结果。已有测试能覆盖时复用并注明。

```sh
go test -race -timeout 60s ./metrics
```

## 实施证据

- [TestNamelessRecordsCountWithoutCreatingSeries](../../../metrics/collector_test.go#L295)

[批次验证与基准](../review-a8c2f28a/README.md)
