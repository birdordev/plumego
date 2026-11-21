# Card 1833 — 核对快照重开后时间平局的淘汰契约

Milestone: 稳定模块质量复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: store
Owned Files: `store/kv/state_regression_test.go`
Depends On: 无

Goal: 重开前后规则一致，过期项按约定滤除。
Scope: 以小历史 fixture 相同时间建立两项，再写入触发容量变化。
Non-goals: 维持稳定公开签名与标准库依赖，将验证限于本卡范围。
Files: `store/kv/state_regression_test.go`。
Tests: 重开前后规则一致，过期项按约定滤除。
Docs Sync: 复核本卡涉及的已公开约定。
Done Definition: 完成下列验收，记录验证命令、状态及剩余限制。
Outcome: 重开后同时间键按相同规则淘汰，过期键从枚举中滤除。

## 证据与影响

第 9 节检查号为 31,32,41。证据状态为补证。

已有同一时间写入的内存回归。重开持久快照后，时间平局的确定性需要另证。 [store/kv/kv.go:436](../../../store/kv/kv.go#L436)

[复审报告](../../../docs/evidence/stable-quality-review-a074eadb.md)

## 验收与验证

- [x] 重开前后规则一致，过期项按约定滤除。
- [x] 记录运行结果。若已有用例覆盖目标，记录用例及相关断言。

```sh
go test -race -timeout 60s ./store/kv
```

## 实施证据

- [TestEvictionPrefersWriteAtSameTimestamp](../../../store/kv/state_regression_test.go#L178)

[批次验证与基准](../review-a074eadb/README.md)
