# Card 1798 — 健康聚合把异常退出的 worker 当成成功

Milestone: 稳定模块质量复审
Recipe: specs/change-recipes/fix-bug.yaml
Context Package: implementation
Priority: P1
State: done
Primary Module: x/observability
Owned Files: `x/observability/ops/healthhttp/manager.go`, `x/observability/ops/healthhttp/aggregate_regression_test.go`, `docs/modules/x/observability/ops/README.md`
Depends On: 无

Goal: 预热后模拟异常退出，旧快照不得令聚合成功；其余 worker 正常结束，后续检查能恢复。
Scope: 每个已调度组件必须产生一次终态。异常退出记录固定失败，聚合按失败或结果缺失判未就绪，保留版本护栏。
Non-goals: 维持稳定公开签名与标准库依赖，将验证限于本卡范围。
Files: `x/observability/ops/healthhttp/manager.go`, `x/observability/ops/healthhttp/aggregate_regression_test.go`, `docs/modules/x/observability/ops/README.md`。
Tests: 预热后模拟异常退出，旧快照不得令聚合成功；其余 worker 正常结束，后续检查能恢复。
Docs Sync: 复核模块说明并验证生成数据。
Done Definition: 完成下列验收，记录验证命令、状态及剩余限制。
Outcome: panic 和 Goexit 记录固定失败，缺失终态也令聚合未就绪。验证剩余检查和下一批均能完成，旧批无法覆盖新就绪状态。

## 证据与影响

第 9 节检查号为 23,28,29,33,42,46。证据状态为已复现。

组件已有健康快照时，后续 Check 调用 Goexit。worker 会归还配额并完成 WaitGroup，但未报告结果，聚合实际返回 healthy 且 Ready=true。后台回调 panic 也缺少本地隔离，后者仅由源码确认。 [x/observability/ops/healthhttp/manager.go:516](../../../x/observability/ops/healthhttp/manager.go#L516)

[复审报告](../../../docs/evidence/stable-quality-review-a074eadb.md)

## 验收与验证

- [x] 预热后模拟异常退出，旧快照不得令聚合成功；其余 worker 正常结束，后续检查能恢复。
- [x] 记录运行结果。若已有用例覆盖目标，记录用例及相关断言。

```sh
go test -race -timeout 60s ./x/observability/ops/healthhttp
make website-sync
make website-check-generated
```

## 实施证据

- [TestAcceptanceAggregateAbnormalChecksHaveFailedOutcomes](../../../x/observability/ops/healthhttp/aggregate_regression_test.go#L64)
- [TestAggregateGeneration](../../../x/observability/ops/healthhttp/aggregate_regression_test.go#L31)

[批次验证与基准](../review-a074eadb/README.md)
