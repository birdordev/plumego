# Card 1902 — 历史统计按追加位置推算时间，跨度可能为负

Milestone: 稳定模块质量复审
Recipe: specs/change-recipes/fix-bug.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: x/observability/ops
Owned Files: `x/observability/ops/healthhttp/tracker.go`, `x/observability/ops/healthhttp/history_test.go`, `docs/modules/x/observability/ops/README.md`
Depends On: 无

Goal: 逆序、混序、同时间和空历史均有可复验结果，列表与分页顺序不受影响。
Scope: 在已有统计遍历中选取时间极值，明确同时间的稳定选择规则，单独复制极值对象。
Non-goals: 维持稳定公开签名与标准库依赖，将验证限于本卡范围。
Files: `x/observability/ops/healthhttp/tracker.go`, `x/observability/ops/healthhttp/history_test.go`, `docs/modules/x/observability/ops/README.md`。
Tests: 逆序、混序、同时间和空历史均有可复验结果，列表与分页顺序不受影响。
Docs Sync: 行为变更后同步清单对应的模块说明并校验生成数据。
Done Definition: 完成下列验收，记录验证命令、状态及剩余限制。
Outcome: 时间统计选择最早和最晚的时间戳，平局采用最先和最后追加的记录。列表保留追加顺序，极值记录是独立快照。

## 证据与影响

第 9 节检查号为 6,31,34,42,46。证据状态为已复现。

注入 Manager 连续返回三条时间递减的普通记录，统计把头尾分别标为 oldest/newest，TimeSpan 为 -2 秒。历史列表按完成追加顺序有意保留，两者语义不同。 [x/observability/ops/healthhttp/tracker.go:638](../../../x/observability/ops/healthhttp/tracker.go#L638)

[复审报告](../../../docs/evidence/stable-quality-review-2b94cf86.md)

## 验收与验证

- [x] 逆序、混序、同时间和空历史均有可复验结果，列表与分页顺序不受影响。
- [x] 记录挂载入口、具体断言和结果。已有测试能覆盖时复用并注明。

```sh
go test -race -timeout 60s ./x/observability/ops/healthhttp
make website-sync
make website-check-generated
```

## 实施证据

- [TestHistoryStatsUsesTimestampExtremaAndPreservesInsertionOrder](../../../x/observability/ops/healthhttp/history_test.go#L382)

[批次验证与基准](../review-2b94cf86/README.md)
