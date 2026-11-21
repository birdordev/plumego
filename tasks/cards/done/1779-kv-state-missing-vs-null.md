# Card 1779 — 核对持久快照缺字段与空对象分类

Milestone: 稳定模块质量复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: store
Owned Files: `store/kv/state_regression_test.go`
Depends On: 无

Goal: 只有支持历史形状正常打开，拒绝后不锁死目录。
Scope: 构造空对象、缺字段、null 三个普通 fixture 并尝试重开。
Non-goals: 维持稳定公开签名与标准库依赖，将验证限于本卡范围。
Files: `store/kv/state_regression_test.go`。
Tests: 只有支持历史形状正常打开，拒绝后不锁死目录。
Docs Sync: 复核本卡涉及的已公开约定。
Done Definition: 完成下列验收，记录验证命令、状态及剩余限制。
Outcome: 空 entries 对象可打开，缺失和 null 返回损坏分类。纠正后可重开目录。

## 证据与影响

第 9 节检查号为 22,31,41,46。证据状态为补证。

解码明确要求 entries 对象。历史兼容与损坏数据的状态需用独立合法或拒绝夹具核对。 [store/kv/persistence.go:42](../../../store/kv/persistence.go#L42)

[复审报告](../../../docs/evidence/stable-quality-review-d224acbc.md)

## 验收与验证

- [x] 只有支持历史形状正常打开，拒绝后不锁死目录。
- [x] 记录运行结果。若已有用例覆盖目标，记录用例及相关断言。

```sh
go test -race -timeout 60s ./store/kv
```

## 实施证据

- [TestEmptySnapshotShapeAndCorruptEntriesRecovery](../../../store/kv/state_regression_test.go#L622)

[批次验证与基准](../review-d224acbc/README.md)
