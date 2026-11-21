# Card 1852 — Tracker 平均耗时计算会把非负样本累积成负数

Milestone: 稳定模块质量复审
Recipe: specs/change-recipes/fix-bug.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: x/observability/ops
Owned Files: `x/observability/ops/healthhttp/tracker.go`, `x/observability/ops/healthhttp/metrics_test.go`, `docs/modules/x/observability/ops/README.md`
Depends On: 无

Goal: 两次极值、交替大小及普通样本，平均值保持在 min/max 内且状态计数不受影响。
Scope: 采用不会形成大中间积的在线平均，明确舍入精度并核对极值。
Non-goals: 维持稳定公开签名与标准库依赖，将验证限于本卡范围。
Files: `x/observability/ops/healthhttp/tracker.go`, `x/observability/ops/healthhttp/metrics_test.go`, `docs/modules/x/observability/ops/README.md`。
Tests: 两次极值、交替大小及普通样本，平均值保持在 min/max 内且状态计数不受影响。
Docs Sync: 行为变更后同步清单对应的模块说明并校验生成数据。
Done Definition: 完成下列验收，记录验证命令、状态及剩余限制。
Outcome: Tracker 用 128 位累计非负耗时，按整纳秒向下取整；零耗时保留在最小值中。Reset 同时清除累计值。

## 证据与影响

第 9 节检查号为 31,35,42,44,46。证据状态为已复现。

注入两个同样的非负 Duration，乘加超出 int64 后平均值为负，落在最小和最大值之外。使用合成极值，生产触发阈值未测定。 [x/observability/ops/healthhttp/tracker.go:351](../../../x/observability/ops/healthhttp/tracker.go#L351)

[复审报告](../../../docs/evidence/stable-quality-review-a8c2f28a.md)

## 验收与验证

- [x] 两次极值、交替大小及普通样本，平均值保持在 min/max 内且状态计数不受影响。
- [x] 记录挂载入口、具体断言和结果。已有测试能覆盖时复用并注明。

```sh
go test -race -timeout 60s ./x/observability/ops/healthhttp
make website-sync
make website-check-generated
```

## 实施证据

- [TestAcceptanceTrackerAverageRetainsIntegerPrecision](../../../x/observability/ops/healthhttp/metrics_test.go#L268)

[批次验证与基准](../review-a8c2f28a/README.md)
