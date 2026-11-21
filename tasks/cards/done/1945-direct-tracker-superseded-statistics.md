# Card 1945 — 核对 Tracker 单组件探测被替换时的统计归属

Milestone: 稳定模块质量复审
Recipe: specs/change-recipes/analysis-only.yaml
Context Package: review
Priority: P2
State: done
Primary Module: x/observability/ops
Owned Files: `x/observability/ops/healthhttp/metrics_test.go`
Depends On: 无

Goal: 区分探测结果和缓存发布结果；确认偏差后再确定最小修复，避免把并发契约假设当成缺陷。
Scope: 以通道控制旧检查晚于新检查结束，比较每个返回值、当前状态与 RecentHistory。
Non-goals: 维持稳定公开签名与标准库依赖，将验证限于本卡范围。
Files: `x/observability/ops/healthhttp/metrics_test.go`。
Tests: 区分探测结果和缓存发布结果；确认偏差后再确定最小修复，避免把并发契约假设当成缺陷。
Docs Sync: 记录核验结论；只在现有契约文字需要澄清时另提文档变更。
Done Definition: 完成下列验收，记录验证命令、状态及剩余限制。
Outcome: 临时探针确认旧检查实际成功却在发布时被替换，Tracker 按发布失败累计，出现成功 0、失败 2。下一步可复用内置检查返回的独立执行结果，同时保留公开返回错误。

## 证据与影响

第 9 节检查号为 28,31,32,42,44。证据状态为补证。

单组件分支执行结束后读取当前组件快照。聚合已改为本批结果，单组件在两个重叠请求更新时的统计仍要界定。 [x/observability/ops/healthhttp/tracker.go:228](../../../x/observability/ops/healthhttp/tracker.go#L228)

[复审报告](../../../docs/evidence/stable-quality-review-2b94cf86.md)

## 验收与验证

- [x] 区分探测结果和缓存发布结果；确认偏差后再确定最小修复，避免把并发契约假设当成缺陷。
- [x] 记录挂载入口、具体断言和结果。已有测试能覆盖时复用并注明。

## 实施证据

阻塞旧检查后完成一项新失败，再让旧检查返回成功。旧返回分类仍是 superseded，两次累计都记为失败。可单独将内置 Tracker.CheckComponent 接到已存在的内部结果参数，用实际执行状态记录统计，并继续交付真实发布错误给调用方。

```sh
go test -race -timeout 40s -v /tmp/2b94-analysis-boundaries_test.go -run '^TestTrackerDirectStatsFollowPublishedSnapshotWhenSuperseded$'
```

[批次验证与基准](../review-2b94cf86/README.md)
