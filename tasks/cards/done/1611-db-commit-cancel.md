# Card 1611 — 补证事务提交遇取消时的错误和终态

Milestone: 本轮质量审查
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: store
Owned Files: `store/db/transaction_regression_test.go`
Depends On: 无

Goal: 补证事务提交遇取消时的错误和终态。
Scope: 复用仓库自定义 driver 控制 Commit 进入及返回。
Non-goals: 保留现有公开签名、依赖与模块边界。测试若证实新的缺陷，先记录证据再单独安排修复。
Files: `store/db/transaction_regression_test.go`。
Tests: 分别验证提交前取消与提交后取消，记录错误分类、Rollback 次数和已提交状态。
Docs Sync: 本卡按现有契约验证。若取证表明契约需调整，先更新后续卡范围。
Done Definition: 完成下列验收和验证，记录证据与剩余风险。
Outcome: 受控驱动证明提交前取消不会提交，驱动提交进入后的取消不反转成功；异步回滚由通道同步，重复 30 次通过。

## 证据与影响

- 检查号：22,26,30,31,41,47。证据状态：补证。
- 事务回调返回后 database/sql 负责执行 Commit，ctx 取消可能与提交同时发生。 [store/db/sql.go:291](../../../store/db/sql.go#L291)
- [审查报告](../../../docs/evidence/stable-quality-review-dee7aa0f.md)

## 验收与验证

- [x] 分别验证提交前取消与提交后取消，记录错误分类、Rollback 次数和已提交状态。
- [x] 运行以下命令并记录结果、失败路径和未验证范围。修改 Go 文件前完成仓库预检，验证前先执行仓库格式化。

```sh
go test -race -timeout 60s ./store/db
```

## 实施证据

[TestCancellationAtTransactionCommitBoundary](../../../store/db/transaction_regression_test.go#L89)
