# Card 1835 — 核对 Commit 失败后事务已结束的错误链

Milestone: 稳定模块质量复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: store
Owned Files: `store/db/transaction_regression_test.go`
Depends On: 无

Goal: 保留提交错误且清理归属明确，第二次事务可用。
Scope: 用本机 driver 计数，模拟 commit 失败和下一次事务。
Non-goals: 维持稳定公开签名与标准库依赖，将验证限于本卡范围。
Files: `store/db/transaction_regression_test.go`。
Tests: 保留提交错误且清理归属明确，第二次事务可用。
Docs Sync: 复核本卡涉及的已公开约定。
Done Definition: 完成下列验收，记录验证命令、状态及剩余限制。
Outcome: 提交失败保留驱动原因并释放连接给下一次事务，驱动未收到额外回滚。

## 证据与影响

第 9 节检查号为 22,29,30,31,41。证据状态为补证。

事务回调、取消和清理错误已有回归。还需核对普通 commit 失败后是否重走 rollback。 [store/db/sql.go:264](../../../store/db/sql.go#L264)

[复审报告](../../../docs/evidence/stable-quality-review-a074eadb.md)

## 验收与验证

- [x] 保留提交错误且清理归属明确，第二次事务可用。
- [x] 记录运行结果。若已有用例覆盖目标，记录用例及相关断言。

```sh
go test -race -timeout 60s ./store/db
```

## 实施证据

- [TestCommitFailureReturnsConnectionWithoutDriverRollback](../../../store/db/transaction_regression_test.go#L182)

[批次验证与基准](../review-a074eadb/README.md)
