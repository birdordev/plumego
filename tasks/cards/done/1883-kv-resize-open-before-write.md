# Card 1883 — 核对以更小容量重开后首次写入的持久边界

Milestone: 稳定模块质量复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: store
Owned Files: `store/kv/state_regression_test.go`
Depends On: 无

Goal: 读取时与磁盘提交时的视图分别明确，已释放锁后可以用任一允许容量重开。
Scope: 以大容量写入两项，再缩容打开并检查内存与磁盘；首次成功写入后恢复原配置比较。
Non-goals: 维持稳定公开签名与标准库依赖，将验证限于本卡范围。
Files: `store/kv/state_regression_test.go`。
Tests: 读取时与磁盘提交时的视图分别明确，已释放锁后可以用任一允许容量重开。
Docs Sync: 记录核验结论；只在现有契约文字需要澄清时另提文档变更。
Done Definition: 完成下列验收，记录验证命令、状态及剩余限制。
Outcome: 窄容量只读重开只影响内存。首次提交替换快照后，宽容量重新打开也只能读取已提交值。

## 证据与影响

第 9 节检查号为 28,30,31,41。证据状态为补证。

重开时按当前容量回收内存项，初始化路径没有立即持久化。需区分只读重开和首次成功提交后重开。 [store/kv/kv.go:103](../../../store/kv/kv.go#L103)

[复审报告](../../../docs/evidence/stable-quality-review-a8c2f28a.md)

## 验收与验证

- [x] 读取时与磁盘提交时的视图分别明确，已释放锁后可以用任一允许容量重开。
- [x] 记录挂载入口、具体断言和结果。已有测试能覆盖时复用并注明。

```sh
go test -race -timeout 60s ./store/kv
```

## 实施证据

- [TestKVCapacityEvictionAtOpenPersistsOnlyWithMutation](../../../store/kv/state_regression_test.go#L780)

[批次验证与基准](../review-a8c2f28a/README.md)
