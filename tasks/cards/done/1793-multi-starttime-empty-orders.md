# Card 1793 — 核对聚合开始时间与空计数的组合

Milestone: 稳定模块质量复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: metrics
Owned Files: `metrics/multi_test.go`
Depends On: 无

Goal: 计数按子快照相加，时间仍取约定的最小非零值。
Scope: 改变子收集器顺序、清空其中一个，然后核对两次快照。
Non-goals: 维持稳定公开签名与标准库依赖，将验证限于本卡范围。
Files: `metrics/multi_test.go`。
Tests: 计数按子快照相加，时间仍取约定的最小非零值。
Docs Sync: 复核本卡涉及的已公开约定。
Done Definition: 完成下列验收，记录验证命令、状态及剩余限制。
Outcome: 空快照、时刻为空及普通计数混合聚合时，按最早非零时间合并，输入顺序不影响结果。

## 证据与影响

第 9 节检查号为 6,32,44。证据状态为补证。

聚合采用最早非零时间，多个时间为空或统计为空但时间非空时需要组合覆盖。 [metrics/multi.go:104](../../../metrics/multi.go#L104)

[复审报告](../../../docs/evidence/stable-quality-review-d224acbc.md)

## 验收与验证

- [x] 计数按子快照相加，时间仍取约定的最小非零值。
- [x] 记录运行结果。若已有用例覆盖目标，记录用例及相关断言。

```sh
go test -race -timeout 60s ./metrics
```

## 实施证据

- [TestAggregateEarliestNonZeroTimeIsIndependentOfOrder](../../../metrics/multi_test.go#L507)

[批次验证与基准](../review-d224acbc/README.md)
