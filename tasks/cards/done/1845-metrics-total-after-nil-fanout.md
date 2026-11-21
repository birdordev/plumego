# Card 1845 — 补证空聚合统计与正常记录同时使用

Milestone: 稳定模块质量复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: metrics
Owned Files: `metrics/multi_test.go`
Depends On: 无

Goal: 空结果 map 可写，正常结果保持独立，清理不污染其他实例。
Scope: 两个独立聚合交叉调用且修改返回的 map。
Non-goals: 维持稳定公开签名与标准库依赖，将验证限于本卡范围。
Files: `metrics/multi_test.go`。
Tests: 空结果 map 可写，正常结果保持独立，清理不污染其他实例。
Docs Sync: 复核本卡涉及的已公开约定。
Done Definition: 完成下列验收，记录验证命令、状态及剩余限制。
Outcome: 空和非空 fan-out 的快照之间相互独立，Clear 不修改已返回的快照。

## 证据与影响

第 9 节检查号为 5,32,34,44。证据状态为补证。

构造已覆盖 nil 过滤，需组合全空与含正常 collector 快照在 Clear 前后的归属。 [metrics/multi.go:82](../../../metrics/multi.go#L82)

[复审报告](../../../docs/evidence/stable-quality-review-a074eadb.md)

## 验收与验证

- [x] 空结果 map 可写，正常结果保持独立，清理不污染其他实例。
- [x] 记录运行结果。若已有用例覆盖目标，记录用例及相关断言。

```sh
go test -race -timeout 60s ./metrics
```

## 实施证据

- [TestEmptyAndLiveFanoutOwnStatsAcrossClear](../../../metrics/multi_test.go#L540)

[批次验证与基准](../review-a074eadb/README.md)
