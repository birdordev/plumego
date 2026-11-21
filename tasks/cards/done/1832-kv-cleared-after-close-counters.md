# Card 1832 — 核对 KV 关闭时快照数据归属和计数

Milestone: 稳定模块质量复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: store
Owned Files: `store/kv/state_regression_test.go`
Depends On: 无

Goal: 内存关闭与磁盘值各符合约定，副本无污染。
Scope: 用普通快照保留读取副本后关闭、重开、读取和重新统计。
Non-goals: 维持稳定公开签名与标准库依赖，将验证限于本卡范围。
Files: `store/kv/state_regression_test.go`。
Tests: 内存关闭与磁盘值各符合约定，副本无污染。
Docs Sync: 复核本卡涉及的已公开约定。
Done Definition: 完成下列验收，记录验证命令、状态及剩余限制。
Outcome: KV 关闭释放内存，已读副本独立，重开恢复磁盘值且读取计数从零开始。

## 证据与影响

第 9 节检查号为 28,29,34,41。证据状态为补证。

KV 与缓存关闭契约不同。需要核对最后读出的副本和已关闭后的统计。 [store/kv/kv.go:408](../../../store/kv/kv.go#L408)

[复审报告](../../../docs/evidence/stable-quality-review-a074eadb.md)

## 验收与验证

- [x] 内存关闭与磁盘值各符合约定，副本无污染。
- [x] 记录运行结果。若已有用例覆盖目标，记录用例及相关断言。

```sh
go test -race -timeout 60s ./store/kv
```

## 实施证据

- [TestClosedKVReleasesPayloadAndReopensIndependentStats](../../../store/kv/state_regression_test.go#L712)

[批次验证与基准](../review-a074eadb/README.md)
