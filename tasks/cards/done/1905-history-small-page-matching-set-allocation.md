# Card 1905 — 一条历史记录的分页请求分配整个匹配集合

Milestone: 稳定模块质量复审
Recipe: specs/change-recipes/fix-bug.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: x/observability/ops
Owned Files: `x/observability/ops/healthhttp/tracker.go`, `x/observability/ops/healthhttp/history_test.go`
Depends On: 1902

Goal: 时间、状态、组件组合筛选及边界页结果等价。对相同样本复测每页分配随页宽变化。
Scope: 扫描时累计匹配数，仅把 offset 后命中的目标页深复制到独立小切片。避免改变过滤、归一化与完整总计语义。
Non-goals: 维持稳定公开签名与标准库依赖，将验证限于本卡范围。
Files: `x/observability/ops/healthhttp/tracker.go`, `x/observability/ops/healthhttp/history_test.go`。
Tests: 时间、状态、组件组合筛选及边界页结果等价。对相同样本复测每页分配随页宽变化。
Docs Sync: 记录核验结论；只在现有契约文字需要澄清时另提文档变更。
Done Definition: 完成下列验收，记录验证命令、状态及剩余限制。
Outcome: 一次扫描统计全部命中，仅深复制目标页。组合筛选、尾页、快照归属和小页分配均已验证。

## 证据与影响

第 9 节检查号为 31,34,35,42,48。证据状态为源码确认。

分页扫描将所有匹配结构复制到临时切片，再用封顶切片返回一页。实测 limit 为 1 时，100 条和 10000 条的中位数分别为 24432 和 3579767 B/op。 [x/observability/ops/healthhttp/tracker.go:601](../../../x/observability/ops/healthhttp/tracker.go#L601)

[复审报告](../../../docs/evidence/stable-quality-review-2b94cf86.md)

## 验收与验证

- [x] 时间、状态、组件组合筛选及边界页结果等价。对相同样本复测每页分配随页宽变化。
- [x] 记录挂载入口、具体断言和结果。已有测试能覆盖时复用并注明。

```sh
go test -race -timeout 60s ./x/observability/ops/healthhttp
go test ./x/observability/ops/healthhttp -run '^$' -bench '^BenchmarkHistoryFirstSmallPage$' -benchmem -benchtime=100ms -count=3
```

## 实施证据

- [TestHistorySmallPagePreservesCombinedFiltersAndOwnership](../../../x/observability/ops/healthhttp/history_test.go#L408)
- [BenchmarkHistoryFirstSmallPage](../../../x/observability/ops/healthhttp/history_test.go#L456)

[批次验证与基准](../review-2b94cf86/README.md)
