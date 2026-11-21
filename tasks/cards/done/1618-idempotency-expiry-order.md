# Card 1618 — 补证幂等完成的错误优先级

Milestone: 本轮质量审查
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: store
Owned Files: `store/idempotency/store_test.go`
Depends On: 无

Goal: 补证幂等完成的错误优先级。
Scope: 用合成时刻补已过期且已完成等组合输入。
Non-goals: 保留现有公开签名、依赖与模块边界。测试若证实新的缺陷，先记录证据再单独安排修复。
Files: `store/idempotency/store_test.go`。
Tests: 错误分类次序明确，恰好过期时的行为可重复；保持单独无效测试。
Docs Sync: 本卡按现有契约验证。若取证表明契约需调整，先更新后续卡范围。
Done Definition: 完成下列验收和验证，记录证据与剩余风险。
Outcome: 组合状态测试用精确到期点验证过期优先于已完成。

## 证据与影响

- 检查号：6,16,22,31,41。证据状态：补证。
- 完成验证依次检查结构、请求摘要、有效期与状态。 [store/idempotency/store.go:135](../../../store/idempotency/store.go#L135)
- [审查报告](../../../docs/evidence/stable-quality-review-dee7aa0f.md)

## 验收与验证

- [x] 错误分类次序明确，恰好过期时的行为可重复；保持单独无效测试。
- [x] 运行以下命令并记录结果、失败路径和未验证范围。修改 Go 文件前完成仓库预检，验证前先执行仓库格式化。

```sh
go test -race -timeout 60s ./store/idempotency
```

## 实施证据

[TestValidateCompletion](../../../store/idempotency/store_test.go#L222)
