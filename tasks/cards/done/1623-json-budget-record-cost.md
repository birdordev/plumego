# Card 1623 — 测量 JSON 节点预算跨字段成本

Milestone: 本轮质量审查
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: log
Owned Files: `log/json_test.go`
Depends On: 无

Goal: 测量 JSON 节点预算跨字段成本。
Scope: 用多字段与单深字段两类基准区分预算层级。
Non-goals: 保留现有公开签名、依赖与模块边界。测试若证实新的缺陷，先记录证据再单独安排修复。
Files: `log/json_test.go`。
Tests: 记录 B/op 和 allocs/op，检查超界字段占位与其他字段继续输出，明确总量策略。
Docs Sync: 本卡按现有契约验证。若取证表明契约需调整，先更新后续卡范围。
Done Definition: 完成下列验收和验证，记录证据与剩余风险。
Outcome: 测量每字段预算的叠加成本，复用现有宽度测试验证超界占位。保持现有字段语义。

## 证据与影响

- 检查号：35,43,48。证据状态：补证。
- 节点预算对每个顶层字段单独计算，存在多字段叠加成本。 [log/json.go:188](../../../log/json.go#L188)
- [审查报告](../../../docs/evidence/stable-quality-review-dee7aa0f.md)

## 验收与验证

- [x] 记录 B/op 和 allocs/op，检查超界字段占位与其他字段继续输出，明确总量策略。
- [x] 运行以下命令并记录结果、失败路径和未验证范围。修改 Go 文件前完成仓库预检，验证前先执行仓库格式化。

```sh
go test -race -timeout 60s ./log
go test ./log -run '^$' -bench 'BenchmarkJSONRecordFieldBudget' -benchmem -count=5
```

## 实施证据

[BenchmarkJSONRecordFieldBudget](../../../log/json_test.go#L807)
