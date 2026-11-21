# Card 1780 — 核对已分类读预算错误后的受限重试

Milestone: 稳定模块质量复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: store
Owned Files: `store/kv/state_regression_test.go`
Depends On: 无

Goal: 计算不溢出，拒绝无写入，合法容量可重试。
Scope: 构造小范围替身测试预算算法与普通文件边界，不分配大快照。
Non-goals: 维持稳定公开签名与标准库依赖，将验证限于本卡范围。
Files: `store/kv/state_regression_test.go`。
Tests: 计算不溢出，拒绝无写入，合法容量可重试。
Docs Sync: 复核本卡涉及的已公开约定。
Done Definition: 完成下列验收，记录验证命令、状态及剩余限制。
Outcome: 小文件边界核对提高读取预算可重试且不写快照，结合历史下限与整数饱和值覆盖。

## 证据与影响

第 9 节检查号为 28,31,41。证据状态为补证。

临界预算随配置计算且有历史默认下限，之前重试覆盖人工改正快照。需核对提高允许值的行为与不会覆盖文件。 [store/kv/persistence.go:65](../../../store/kv/persistence.go#L65)

[复审报告](../../../docs/evidence/stable-quality-review-d224acbc.md)

## 验收与验证

- [x] 计算不溢出，拒绝无写入，合法容量可重试。
- [x] 记录运行结果。若已有用例覆盖目标，记录用例及相关断言。

```sh
go test -race -timeout 60s ./store/kv
```

## 实施证据

- [TestSnapshotBudgetWideningAllowsReadWithoutWriting](../../../store/kv/state_regression_test.go#L666)
- [TestOversizedSnapshotStartupReleasesDirectoryLock](../../../store/kv/state_regression_test.go#L583)

[批次验证与基准](../review-d224acbc/README.md)
