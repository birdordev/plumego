# Card 2002 — 核对单组件 Tracker 在关闭时保留已执行计量

Milestone: 稳定模块质量复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: x/observability/ops
Owned Files: `x/observability/ops/healthhttp/metrics_test.go`
Depends On: 1954

Goal: 保留关闭分类，缓存不再发布，统计只计实际执行。
Scope: 用通道阻塞正常回调，在释放前关闭 Manager，再执行一次关闭后的调用。
Non-goals: 维持稳定公开签名与标准库依赖，将验证限于本卡范围。
Files: `x/observability/ops/healthhttp/metrics_test.go`。
Tests: 保留关闭分类，缓存不再发布，统计只计实际执行。
Docs Sync: 记录核验结论；只在现有契约文字需要澄清时另提文档变更。
Done Definition: 完成下列验收，记录验证命令、状态及剩余限制。
Outcome: 关闭后在途直接检查保留执行计量且不再更新已有快照；再发起检查返回关闭分类且不计数。

## 证据与影响

第 9 节检查号为 22,26,28,42,44,47，当前证据状态为补证。 [质量规则第 9 节](../../../docs/operations/agent-code-quality-rules.md#L267)

Close 可发生在回调完成和发布之间。1954 固定尝试计量后，还需区分关闭前真正执行与关闭后未执行。 [x/observability/ops/healthhttp/tracker.go:228](../../../x/observability/ops/healthhttp/tracker.go#L228)

[复审报告](../../../docs/evidence/stable-quality-review-999d5e4d.md)

## 验收与验证

- [x] 保留关闭分类，缓存不再发布，统计只计实际执行。
- [x] 记录挂载入口、具体断言和结果。已有测试能覆盖时复用并注明。

```sh
go test -race -timeout 60s ./x/observability/ops/healthhttp
```

## 实施证据

- [TestTrackerCloseKeepsExecutedDirectAttemptMetrics](../../../x/observability/ops/healthhttp/metrics_test.go#L485)
