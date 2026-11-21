# Card 1906 — 历史导出说明缺少分页默认值与超界归一化

Milestone: 稳定模块质量复审
Recipe: specs/change-recipes/fix-bug.yaml
Context Package: implementation
Priority: P3
State: done
Primary Module: x/observability/ops
Owned Files: `docs/modules/x/observability/ops/README.md`, `x/observability/ops/healthhttp/history_test.go`
Depends On: 1905

Goal: 文档字段与缺省、负值、100 和 101 的 HTTP 观察一致，网站生成检查通过。
Scope: 补齐整数有效区间、归一化和非法字面值错误，不调整阈值或线上返回形状。
Non-goals: 维持稳定公开签名与标准库依赖，将验证限于本卡范围。
Files: `docs/modules/x/observability/ops/README.md`, `x/observability/ops/healthhttp/history_test.go`。
Tests: 文档字段与缺省、负值、100 和 101 的 HTTP 观察一致，网站生成检查通过。
Docs Sync: 行为变更后同步清单对应的模块说明并校验生成数据。
Done Definition: 完成下列验收，记录验证命令、状态及剩余限制。
Outcome: 公开文档写明 1–100、50 默认页宽和负偏移归零；测试实际 HTTP JSON 的分页字段。

## 证据与影响

第 9 节检查号为 6,14,16,42,52。证据状态为源码确认。

说明只写了整数和缺省值的处理；实现把 1–100 以外的 limit 归为 50，负 offset 归为 0。现有描述无法直接推导边界行为。 [x/observability/ops/healthhttp/tracker.go:581](../../../x/observability/ops/healthhttp/tracker.go#L581)

[复审报告](../../../docs/evidence/stable-quality-review-2b94cf86.md)

## 验收与验证

- [x] 文档字段与缺省、负值、100 和 101 的 HTTP 观察一致，网站生成检查通过。
- [x] 记录挂载入口、具体断言和结果。已有测试能覆盖时复用并注明。

```sh
go test -race -timeout 60s ./x/observability/ops/healthhttp
make website-sync
make website-check-generated
```

## 实施证据

- [TestHistoryExportReportsDocumentedPaginationLimits](../../../x/observability/ops/healthhttp/history_test.go#L439)

[批次验证与基准](../review-2b94cf86/README.md)
