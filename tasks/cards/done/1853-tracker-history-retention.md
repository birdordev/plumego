# Card 1853 — 每次健康聚合都追加历史且没有保留上限

Milestone: 稳定模块质量复审
Recipe: specs/change-recipes/analysis-only.yaml
Context Package: review
Priority: P2
State: done
Primary Module: x/observability/ops
Owned Files: `x/observability/ops/healthhttp/tracker.go`, `x/observability/ops/healthhttp/history_test.go`
Depends On: 无

Goal: 给出容量与留存量的本机测量，明确配置默认和调用方兼容，记录分页、统计及 Reset 的下一步验收。
Scope: 先核对宿主中使用历史的调用方。设计由宿主配置的条目上限或明确的保留策略，落实前记录默认值和兼容决策。
Non-goals: 维持稳定公开签名与标准库依赖，将验证限于本卡范围。
Files: `x/observability/ops/healthhttp/tracker.go`, `x/observability/ops/healthhttp/history_test.go`。
Tests: 给出容量与留存量的本机测量，明确配置默认和调用方兼容，记录分页、统计及 Reset 的下一步验收。
Docs Sync: 记录核验结论；只在现有契约文字需要澄清时另提文档变更。
Done Definition: 完成下列验收，记录验证命令、状态及剩余限制。
Outcome: 核对 Tracker 和列表、导出、统计入口。本库现有调用方没有启用截断语义。用 100、10000 次无外部依赖的模拟检查测得当前历史和复制成本。兼容方案采用未来新增的可选有限条目策略，旧构造保持原语义，容量淘汰顺序和统计含义在扩展契约确定后实施。

## 证据与影响

第 9 节检查号为 14,29,35,42,48。证据状态为源码确认。

运行期每次 CheckAllComponents 都追加一条，释放路径为显式 Reset 或实例生命周期结束。分页只限制返回页，历史保留量与总检查次数同阶。 [x/observability/ops/healthhttp/tracker.go:257](../../../x/observability/ops/healthhttp/tracker.go#L257)

[复审报告](../../../docs/evidence/stable-quality-review-a8c2f28a.md)

## 验收与验证

在工作区外准备只读统计桩，并用 Go overlay 将基准加入探测包后运行。保留源码原状。

- [x] 给出容量与留存量的本机测量，明确配置默认和调用方兼容，记录分页、统计及 Reset 的下一步验收。
- [x] 记录挂载入口、具体断言和结果。已有测试能覆盖时复用并注明。

## 实施证据

用工作区外的 Go 基准创建内置 Manager 和 Tracker，注册一个立即成功的合成组件。分别执行 100 次和 10000 次聚合，随后每次基准迭代读取并检查全量历史条数。

可选策略拟定为 0 保留现有全部历史，正数保留最近 N 个完成记录。满容量时逐出最早记录并释放组件切片引用。列表、导出及统计都基于同一留存窗口，Reset 清空该窗口。新配置入口在扩展调用方接受查询范围及留存语义后单独实现。

```sh
go test -run '^$' -bench '^BenchmarkTrackerHistoryRetention$' -benchmem -benchtime=100ms -count=3 /tmp/a8c-retention-boundaries_test.go
```

[批次验证与基准](../review-a8c2f28a/README.md)
