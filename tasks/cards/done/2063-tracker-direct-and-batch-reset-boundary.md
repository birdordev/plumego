# Card 2063 — 直查与批次跨 Reset 的计量边界

Milestone: 稳定模块第 9 节复审
Recipe: specs/change-recipes/analysis-only.yaml
Context Package: review
Priority: P2
State: done
Primary Module: x/observability/ops
Owned Files: `x/observability/ops/healthhttp/metrics_test.go`
Depends On: 无

Goal: 直查与批次跨 Reset 的计量边界
Scope: 使用两个无副作用的阻塞检查和通道，由测试控制 Reset 时序。
Non-goals: 范围限于本卡涉及的契约，保持公开签名和依赖。
Files: `x/observability/ops/healthhttp/metrics_test.go`。
Tests: 记录开始与完成顺序、按各入口断言重置后快照和历史的口径，行为歧义另列。
Docs Sync: 核对新增验收与已有模块说明一致，记录证据和限制。
Done Definition: 完成以下验收，记录命令、结果与剩余限制。
Outcome: 仅分析。Reset 清空当时已记录的结果。Reset 前启动、之后完成的直查和批次都会进入新统计，只有批次写聚合历史。

## 证据与影响

证据状态为补证。第 9 节检查号为 28,31,32,42,47。 [质量规则第 9 节](../../../docs/operations/agent-code-quality-rules.md#L267)

Reset 在单锁中清空当前统计，进行中的回调仍可随后提交计数。需要定义重置作用于观测还是启动时间。 [x/observability/ops/healthhttp/tracker.go:530](../../../x/observability/ops/healthhttp/tracker.go#L530)

[复审报告](../../../docs/evidence/stable-quality-review-c3a77410.md)

## 验收与验证

- [x] 记录开始与完成顺序、按各入口断言重置后快照和历史的口径，行为歧义另列。
- [x] 验收以记录观察结果和兼容性判断为准。

## 实施证据

用通道等直查或批次首个回调进入，Reset 后再放行。两组 CheckCount 都从 0 到 1。晚完成的直查没有批次历史，晚完成的批次有一条。 临时探针通过，工作区内未新增对应实现。

```sh
go test -race -count=1 -v -overlay=/tmp/c3a-impl-analysis-overlay.json ./x/observability/ops/healthhttp -run '^TestReviewTrackerResetIncludesOutcomesRecordedAfterReset$'
```
