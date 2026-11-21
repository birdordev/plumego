# Card 1612 — 记录 ScanRows 结果集边界

Milestone: 本轮质量审查
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: store
Owned Files: `store/db/sql.go`, `store/db/sql_test.go`
Depends On: 无

Goal: 记录 ScanRows 结果集边界。
Scope: 用受控两结果集 driver 先锁定现契约，再明确 GoDoc。
Non-goals: 保留现有公开签名、依赖与模块边界。测试若证实新的缺陷，先记录证据再单独安排修复。
Files: `store/db/sql.go`, `store/db/sql_test.go`。
Tests: 验证首结果集范围、关闭次数以及末尾错误处理。
Docs Sync: 本卡按现有契约验证。若取证表明契约需调整，先更新后续卡范围。
Done Definition: 完成下列验收和验证，记录证据与剩余风险。
Outcome: 明确 ScanRows 只读取当前结果集，验证提前关闭保留 close 错误。

## 证据与影响

- 检查号：6,16,22,29,41。证据状态：补证。
- QueryRowStrict 显式遍历结果集，ScanRows 循环只使用 Next，注释未明确与多结果集的关系。 [store/db/sql.go:376](../../../store/db/sql.go#L376)
- [审查报告](../../../docs/evidence/stable-quality-review-dee7aa0f.md)

## 验收与验证

- [x] 验证首结果集范围、关闭次数以及末尾错误处理。
- [x] 运行以下命令并记录结果、失败路径和未验证范围。修改 Go 文件前完成仓库预检，验证前先执行仓库格式化。

```sh
go test -race -timeout 60s ./store/db
```

## 实施证据

[TestScanRowsClosesWithoutAdvancingResultSets](../../../store/db/sql_test.go#L1416)
