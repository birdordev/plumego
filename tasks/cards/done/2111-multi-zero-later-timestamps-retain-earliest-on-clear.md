# Card 2111 — 聚合子收集器重置形成的新起点

Milestone: 稳定模块第 9 节复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: metrics
Owned Files: `metrics/multi_test.go`
Depends On: 无

Goal: 聚合子收集器重置形成的新起点
Scope: 让一个子项清空到较晚时刻，另一个保留已知起点，再引入零起点。
Non-goals: 范围限于本卡涉及的契约，保持公开签名和依赖。
Files: `metrics/multi_test.go`。
Tests: 新聚合起点仍为最早非零子项，清空不会修改已取出的旧快照。
Docs Sync: 核对新增验收与已有模块说明一致，记录证据和限制。
Done Definition: 完成以下验收，记录命令、结果与剩余限制。
Outcome: 使用确定性时钟越过一次子收集器 Clear。聚合起点变成剩余最早的非零子项，旧快照独立。

## 证据与影响

证据状态为补证。第 9 节检查号为 28,32,44,47。 [质量规则第 9 节](../../../docs/operations/agent-code-quality-rules.md#L267)

聚合忽略未知起点，并从独立子快照选择最早已知起点。 [metrics/multi.go:82](../../../metrics/multi.go#L82)

[复审报告](../../../docs/evidence/stable-quality-review-2f962d21.md)

## 验收与验证

- [x] 新聚合起点仍为最早非零子项，清空不会修改已取出的旧快照。
- [x] 已有测试覆盖相同触发和断言时可直接复用，记录具体用例。

```sh
go test -race -timeout 60s ./metrics
```

## 实施证据

- [TestAggregateStartTimeMovesToEarliestRemainingChildAfterClear](../../../metrics/multi_test.go#L636)
