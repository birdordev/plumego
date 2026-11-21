# Card 1890 — 确定 Tracker Reset 与在途检查的发布边界

Milestone: 稳定模块质量复审
Recipe: specs/change-recipes/analysis-only.yaml
Context Package: review
Priority: P2
State: done
Primary Module: x/observability/ops
Owned Files: `x/observability/ops/healthhttp/metrics_test.go`
Depends On: 1848

Goal: 给出可重复观察的当前语义。若需按代隔离再单独列修复，避免无依据丢历史。
Scope: 用可控 Manager 令开始于 Reset 前的检查在 Reset 后完成，分别查询指标、历史和开始时间。
Non-goals: 维持稳定公开签名与标准库依赖，将验证限于本卡范围。
Files: `x/observability/ops/healthhttp/metrics_test.go`。
Tests: 给出可重复观察的当前语义。若需按代隔离再单独列修复，避免无依据丢历史。
Docs Sync: 记录核验结论；只在现有契约文字需要澄清时另提文档变更。
Done Definition: 完成下列验收，记录验证命令、状态及剩余限制。
Outcome: 检查开始后、结果发布前 Reset。临时探针确认在途检查完成时计入新周期，开始时间仍为 Reset 时刻。组件计数和历史追加按各自锁段发布，不新增代际丢弃。

## 证据与影响

第 9 节检查号为 28,31,32,42。证据状态为补证。

Reset 清空指标和历史，组件计数与历史追加使用分开锁段。与在途批次交叠时，新统计属于哪个周期需验证。 [x/observability/ops/healthhttp/tracker.go:516](../../../x/observability/ops/healthhttp/tracker.go#L516)

[复审报告](../../../docs/evidence/stable-quality-review-a8c2f28a.md)

## 验收与验证

如果现有用例未覆盖该交叠，用工作区外临时桩或 Go overlay 补充取证。

- [x] 给出可重复观察的当前语义。若需按代隔离再单独列修复，避免无依据丢历史。
- [x] 记录挂载入口、具体断言和结果。已有测试能覆盖时复用并注明。

## 实施证据

用可控组件在 Check 中阻塞，主 goroutine 等待其进入后调用 Reset，读取新开始时间及空统计。释放 Check 后重读统计与历史。观测到 CheckCount 由 0 变为 1，历史新增 1 条，开始时间保持 Reset 时刻。

```sh
go test -race -timeout 30s /tmp/a8c-retention-boundaries_test.go -run '^TestResetDuringProbePublishesWhenItFinishes$'
```

[批次验证与基准](../review-a8c2f28a/README.md)
