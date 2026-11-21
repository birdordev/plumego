# Card 2068 — 明确事务回调的提交与回滚所有权

Milestone: 稳定模块第 9 节复审
Recipe: specs/change-recipes/fix-bug.yaml
Context Package: implementation
Priority: P3
State: done
Primary Module: store/db
Owned Files: `store/db/sql.go`, `store/db/transaction_regression_test.go`
Depends On: 无

Goal: 明确事务回调的提交与回滚所有权
Scope: 注明回调负责查询和写入，结束事务由 helper 负责。保留现有错误链和分类，避免把 ErrTxDone 一概当成功。
Non-goals: 范围限于本卡涉及的契约，保持公开签名和依赖。
Files: `store/db/sql.go`, `store/db/transaction_regression_test.go`。
Tests: 用现有事务桩记录手动结束后外层的分类与驱动操作次数，另确认普通回调完成继续提交一次。
Docs Sync: 本卡同步所属公开入口或字段 GoDoc。
Done Definition: 完成以下验收，记录命令、结果与剩余限制。
Outcome: 完成。GoDoc 明确结束事务由 helper 负责。驱动桩验证普通提交一次，回调自行结束后外层保留 ErrTxDone 类型。

## 证据与影响

证据状态为源码确认。第 9 节检查号为 16,22,29,31,41。 [质量规则第 9 节](../../../docs/operations/agent-code-quality-rules.md#L267)

WithTransaction 根据回调返回值调用 Commit 或 Rollback。回调接收可自行结束事务的 *sql.Tx，若已提交，外层再次 Commit 只看到 sql.ErrTxDone，不能据此判断实际提交结果。公开说明未提醒调用方。 [store/db/sql.go:258](../../../store/db/sql.go#L258)

[复审报告](../../../docs/evidence/stable-quality-review-2f962d21.md)

## 验收与验证

- [x] 用现有事务桩记录手动结束后外层的分类与驱动操作次数，另确认普通回调完成继续提交一次。
- [x] 已有测试覆盖相同触发和断言时可直接复用，记录具体用例。

```sh
go test -race -timeout 60s ./store/db
```

## 实施证据

- [TestWithTransactionOwnsCommitAndRollback](../../../store/db/transaction_regression_test.go#L208)
