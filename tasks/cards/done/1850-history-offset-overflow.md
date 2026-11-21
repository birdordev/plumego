# Card 1850 — 超出历史尾部的大 offset 错报还有下一页

Milestone: 稳定模块质量复审
Recipe: specs/change-recipes/fix-bug.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: x/observability/ops
Owned Files: `x/observability/ops/healthhttp/tracker.go`, `x/observability/ops/healthhttp/history_test.go`, `docs/modules/x/observability/ops/README.md`
Depends On: 无

Goal: 固定集合核对空集、末页、恰等于总数、最大 int 和普通下一页，保持 Total、Limit、Offset 字段语义。
Scope: 先比较 offset 与条目数。仅在有效区间内，用剩余条目数限制页宽后计算结束位置。
Non-goals: 维持稳定公开签名与标准库依赖，将验证限于本卡范围。
Files: `x/observability/ops/healthhttp/tracker.go`, `x/observability/ops/healthhttp/history_test.go`, `docs/modules/x/observability/ops/README.md`。
Tests: 固定集合核对空集、末页、恰等于总数、最大 int 和普通下一页，保持 Total、Limit、Offset 字段语义。
Docs Sync: 行为变更后同步清单对应的模块说明并校验生成数据。
Done Definition: 完成下列验收，记录验证命令、状态及剩余限制。
Outcome: 先以剩余条目数限制页宽，再计算结束位置。末页、尾后与最大 int 偏移均正确报告无下一页。

## 证据与影响

第 9 节检查号为 14,18,38,42。证据状态为已复现。

历史仅一项时，以平台最大 int 为 offset，页为空但 HasMore 为 true。offset 加 limit 溢出，客户端据此错误继续翻页。 [x/observability/ops/healthhttp/tracker.go:618](../../../x/observability/ops/healthhttp/tracker.go#L618)

[复审报告](../../../docs/evidence/stable-quality-review-a8c2f28a.md)

## 验收与验证

- [x] 固定集合核对空集、末页、恰等于总数、最大 int 和普通下一页，保持 Total、Limit、Offset 字段语义。
- [x] 记录挂载入口、具体断言和结果。已有测试能覆盖时复用并注明。

```sh
go test -race -timeout 60s ./x/observability/ops/healthhttp
make website-sync
make website-check-generated
```

## 实施证据

- [TestAcceptanceHistoryOffsetsPastEndHaveNoMore](../../../x/observability/ops/healthhttp/history_test.go#L305)

[批次验证与基准](../review-a8c2f28a/README.md)
