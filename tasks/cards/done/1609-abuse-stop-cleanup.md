# Card 1609 — 补证 limiter Stop 的多调用者同步

Milestone: 本轮质量审查
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: security
Owned Files: `security/abuse/limiter_test.go`
Depends On: 无

Goal: 补证 limiter Stop 的多调用者同步。
Scope: 用通道触发清理退出，替代依赖实际 sleep 的生命周期断言。
Non-goals: 保留现有公开签名、依赖与模块边界。测试若证实新的缺陷，先记录证据再单独安排修复。
Files: `security/abuse/limiter_test.go`。
Tests: 两个 Stop 调用均等待清理完成，后续 Allow 的约定清晰。
Docs Sync: 本卡按现有契约验证。若取证表明契约需调整，先更新后续卡范围。
Done Definition: 完成下列验收和验证，记录证据与剩余风险。
Outcome: 两个 Stop 都等到清理退出，结束后 Allow 仍可用，GoDoc 写明约定。

## 证据与影响

- 检查号：28,29,32,40,47。证据状态：补证。
- Stop 使用 sync.Once 和 stoppedCh，不直接表达后续 Allow 的现有约定。 [security/abuse/limiter.go:438](../../../security/abuse/limiter.go#L438)
- [审查报告](../../../docs/evidence/stable-quality-review-dee7aa0f.md)

## 验收与验证

- [x] 两个 Stop 调用均等待清理完成，后续 Allow 的约定清晰。
- [x] 运行以下命令并记录结果、失败路径和未验证范围。修改 Go 文件前完成仓库预检，验证前先执行仓库格式化。

```sh
go test -race -timeout 60s ./security/abuse
```

## 实施证据

[TestConcurrentStopWaitsForCleanupAndKeepsAdmissionUsable](../../../security/abuse/limiter_test.go#L843)
