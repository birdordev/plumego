# Card 2139 — 核对取消与事务回滚错误的分类

Milestone: 稳定模块第 9 节复审
Recipe: specs/change-recipes/analysis-only.yaml
Context Package: review
Priority: P2
State: done
Primary Module: store/db
Owned Files: `store/db/transaction_regression_test.go`
Depends On: 无

Goal: 核对取消与事务回滚错误的分类。
Scope: 使用现有数据库驱动替身区分 helper 主动回滚和 database/sql 自动回滚的所有权。
Non-goals: 本卡提交取证结果。后续行为变更由独立修复卡执行。
Files: `store/db/transaction_regression_test.go`
Tests: 断言可分类的主错误及实际可见的清理错误，一次结束事务和连接归还。记录无法从驱动回传的分支。
Docs Sync: 核对既有文档与验收结果一致，记录需要后续同步的位置。
Done Definition: 记录命令、结果、证据限制和是否需要独立修复卡。
Outcome: 受控驱动明确区分回滚所有权。helper 主动回滚可同时分类取消及驱动主因；database/sql 自动回滚时，helper 仍保留回调取消，驱动回滚原因由数据库层消化。现有测试确认连接归还。

## 证据与影响

证据状态为待补证。第 9 节检查号为 22,26,29,30,31,41,46。已有提交取消和回滚所有权测试。回调到期且驱动回滚同时报错时，调用端可观察的错误链和连接复用待核对。 [store/db/sql.go:273](../../../store/db/sql.go#L273) [store/db/sql.go:293](../../../store/db/sql.go#L293) [store/db/transaction_regression_test.go:90](../../../store/db/transaction_regression_test.go#L90)

[复审报告](../../../docs/evidence/stable-quality-review-6e8d53f9.md)

## 验收与验证

- [x] 断言可分类的主错误及实际可见的清理错误，一次结束事务和连接归还。记录无法从驱动回传的分支。
- [x] 记录命令、结果、证据限制和是否需要独立修复卡。

```sh
go test -race -timeout 60s ./store/db
```
