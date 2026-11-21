# Card 1614 — 补证容量失败后 Append 的字节和 TTL

Milestone: 本轮质量审查
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: store
Owned Files: `store/cache/state_regression_test.go`
Depends On: 无

Goal: 补证容量失败后 Append 的字节和 TTL。
Scope: 使用可控时间和小配额扩充已有状态回归。
Non-goals: 保留现有公开签名、依赖与模块边界。测试若证实新的缺陷，先记录证据再单独安排修复。
Files: `store/cache/state_regression_test.go`。
Tests: 失败不改变值、有效期或占用，后续合法 Append 仍能提交。
Docs Sync: 本卡按现有契约验证。若取证表明契约需调整，先更新后续卡范围。
Done Definition: 完成下列验收和验证，记录证据与剩余风险。
Outcome: 容量拒绝保留值、原 TTL 与统计，合法追加仍可提交，仍在原期限到期。

## 证据与影响

- 检查号：31,34,35,41,46。证据状态：补证。
- Append 会事先检查内存并保留既有失效时间，拒绝路径需两个不变量同测。 [store/cache/memory.go:417](../../../store/cache/memory.go#L417)
- [审查报告](../../../docs/evidence/stable-quality-review-dee7aa0f.md)

## 验收与验证

- [x] 失败不改变值、有效期或占用，后续合法 Append 仍能提交。
- [x] 运行以下命令并记录结果、失败路径和未验证范围。修改 Go 文件前完成仓库预检，验证前先执行仓库格式化。

```sh
go test -race -timeout 60s ./store/cache
```

## 实施证据

[TestAppendCapacityFailurePreservesExpiration](../../../store/cache/state_regression_test.go#L184)
