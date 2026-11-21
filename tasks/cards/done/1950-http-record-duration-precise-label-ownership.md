# Card 1950 — 核对 HTTP Record 的秒值、纳秒时长和标签归属

Milestone: 稳定模块质量复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: metrics
Owned Files: `metrics/collector_test.go`
Depends On: 无

Goal: 秒值和 Duration 同源，map 可独立修改，固定标签集合明确。
Scope: 用小数纳秒换算值和 0 构造两条 record，变更其中一条 labels，比较另一条及 Duration。
Non-goals: 维持稳定公开签名与标准库依赖，将验证限于本卡范围。
Files: `metrics/collector_test.go`。
Tests: 秒值和 Duration 同源，map 可独立修改，固定标签集合明确。
Docs Sync: 记录核验结论；只在现有契约文字需要澄清时另提文档变更。
Done Definition: 完成下列验收，记录验证命令、状态及剩余限制。
Outcome: HTTP Record 同时保留精确 Duration 和秒值，两条记录的标签各自拥有，固定三类标签没有混入字节数。

## 证据与影响

第 9 节检查号为 6,9,34,44。证据状态为补证。

HTTP record 同时保留整纳秒和秒浮点表示，字节不作为标签。需小数秒和零秒同时对照独立标签。 [metrics/collector.go:48](../../../metrics/collector.go#L48)

[复审报告](../../../docs/evidence/stable-quality-review-2b94cf86.md)

## 验收与验证

- [x] 秒值和 Duration 同源，map 可独立修改，固定标签集合明确。
- [x] 记录挂载入口、具体断言和结果。已有测试能覆盖时复用并注明。

```sh
go test -race -timeout 60s ./metrics
```

## 实施证据

- [TestHTTPRecordOwnsLabelsAndRetainsSubsecondDuration](../../../metrics/collector_test.go#L312)

[批次验证与基准](../review-2b94cf86/README.md)
