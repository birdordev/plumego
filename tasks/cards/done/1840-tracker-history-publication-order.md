# Card 1840 — 核对 tracker 的完成先后与历史顺序

Milestone: 稳定模块质量复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: x/observability
Owned Files: `x/observability/ops/healthhttp/history_test.go`
Depends On: 无

Goal: 较旧检查不能把当前就绪回退，历史顺序可预测。
Scope: 本机合成 Manager 控制先后，比较当前 readiness 与历史顺序。
Non-goals: 维持稳定公开签名与标准库依赖，将验证限于本卡范围。
Files: `x/observability/ops/healthhttp/history_test.go`。
Tests: 较旧检查不能把当前就绪回退，历史顺序可预测。
Docs Sync: 复核本卡涉及的已公开约定。
Done Definition: 完成下列验收，记录验证命令、状态及剩余限制。
Outcome: 重叠批次的历史按完成顺序追加，旧批失败结果不回退已发布的 readiness。

## 证据与影响

第 9 节检查号为 28,31,32,42。证据状态为补证。

内置 manager 有版本护栏，tracker 读取状态后还追加历史。需联合观察两次批量重叠。 [x/observability/ops/healthhttp/tracker.go:237](../../../x/observability/ops/healthhttp/tracker.go#L237)

[复审报告](../../../docs/evidence/stable-quality-review-a074eadb.md)

## 验收与验证

- [x] 较旧检查不能把当前就绪回退，历史顺序可预测。
- [x] 记录运行结果。若已有用例覆盖目标，记录用例及相关断言。

```sh
go test -race -timeout 60s ./x/observability/ops/healthhttp
```

## 实施证据

- [TestTrackerRecordsCompletionOrderWithoutReplacingNewerReadiness](../../../x/observability/ops/healthhttp/history_test.go#L273)

[批次验证与基准](../review-a074eadb/README.md)
