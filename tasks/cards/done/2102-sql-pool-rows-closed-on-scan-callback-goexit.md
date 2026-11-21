# Card 2102 — SQL 扫描回调异常退出释放结果集

Milestone: 稳定模块第 9 节复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: store/db
Owned Files: `store/db/transaction_regression_test.go`
Depends On: 无

Goal: SQL 扫描回调异常退出释放结果集
Scope: 在隔离 goroutine 用已有普通行桩触发 Goexit，等退出后读取 Close 与连接计数。
Non-goals: 范围限于本卡涉及的契约，保持公开签名和依赖。
Files: `store/db/transaction_regression_test.go`。
Tests: Close 恰一次，连接返回池中，后续查询可用。
Docs Sync: 核对新增验收与已有模块说明一致，记录证据和限制。
Done Definition: 完成以下验收，记录命令、结果与剩余限制。
Outcome: 复用扫描器异常退出回归并补连接可用断言。Goexit 和 panic 均关闭一次结果集，池占用归零。

## 证据与影响

证据状态为补证。第 9 节检查号为 29,30,31,41,47。 [质量规则第 9 节](../../../docs/operations/agent-code-quality-rules.md#L267)

ScanRows 的延迟关闭同时覆盖回调异常退出。 [store/db/sql.go:386](../../../store/db/sql.go#L386)

[复审报告](../../../docs/evidence/stable-quality-review-2f962d21.md)

## 验收与验证

- [x] Close 恰一次，连接返回池中，后续查询可用。
- [x] 已有测试覆盖相同触发和断言时可直接复用，记录具体用例。

```sh
go test -race -timeout 60s ./store/db
```

## 实施证据

- [TestRowsCallbacksCloseOnPanicAndGoexit](../../../store/db/transaction_regression_test.go#L133)
