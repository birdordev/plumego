# Card 1901 — 奇数样本窗口把全成功组件标成改善

Milestone: 稳定模块质量复审
Recipe: specs/change-recipes/fix-bug.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: x/observability/ops
Owned Files: `x/observability/ops/healthhttp/tracker.go`, `x/observability/ops/healthhttp/metrics_test.go`, `docs/modules/x/observability/ops/README.md`
Depends On: 无

Goal: 全成功、全失败、改善和下降对照 3、4、9、10 条窗口，得分介于 -1 与 1。
Scope: 分别用所属半区的样本数计算成功率，保留阈值与小于 3 条时的初始规则。
Non-goals: 维持稳定公开签名与标准库依赖，将验证限于本卡范围。
Files: `x/observability/ops/healthhttp/tracker.go`, `x/observability/ops/healthhttp/metrics_test.go`, `docs/modules/x/observability/ops/README.md`。
Tests: 全成功、全失败、改善和下降对照 3、4、9、10 条窗口，得分介于 -1 与 1。
Docs Sync: 行为变更后同步清单对应的模块说明并校验生成数据。
Done Definition: 完成下列验收，记录验证命令、状态及剩余限制。
Outcome: 奇数窗口的两半分别使用各自长度，修复全成功仍报告改善。验证 3、4、9、10 个样本的四种趋势。

## 证据与影响

第 9 节检查号为 6,12,42,44,46。证据状态为已复现。

三个检查全部成功时 TrendScore 为 1.5，Direction 为 improving。新半区与旧半区样本数不同，计算分母用反。 [x/observability/ops/healthhttp/tracker.go:399](../../../x/observability/ops/healthhttp/tracker.go#L399)

[复审报告](../../../docs/evidence/stable-quality-review-2b94cf86.md)

## 验收与验证

- [x] 全成功、全失败、改善和下降对照 3、4、9、10 条窗口，得分介于 -1 与 1。
- [x] 记录挂载入口、具体断言和结果。已有测试能覆盖时复用并注明。

```sh
go test -race -timeout 60s ./x/observability/ops/healthhttp
make website-sync
make website-check-generated
```

## 实施证据

- [TestTrackerTrendUsesEachHalfLength](../../../x/observability/ops/healthhttp/metrics_test.go#L366)

[批次验证与基准](../review-2b94cf86/README.md)
