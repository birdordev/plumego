# Card 1584 — 补证启动取消与回滚错误组合

Milestone: 本轮质量审查
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: core
Owned Files: `core/lifecycle_group_test.go`
Depends On: 无

Goal: 补证启动取消与回滚错误组合。
Scope: 复用已存在的回滚和取消测试夹具。
Non-goals: 保留现有公开签名、依赖与模块边界。测试若证实新的缺陷，先记录证据再单独安排修复。
Files: `core/lifecycle_group_test.go`。
Tests: 断言 ctx 错误和 Stop 错误均可用 errors.Is 分类，下次 Stop 只重试待清理组件。
Docs Sync: 本卡按现有契约验证。若取证表明契约需调整，先更新后续卡范围。
Done Definition: 完成下列验收和验证，记录证据与剩余风险。
Outcome: 复用了已有受控回归，明确同时保留 context 和回滚错误，并只重试一个待清理组件。

## 证据与影响

- 检查号：22,26,30,36,46。证据状态：补证。
- 启动取消与部分成功检查各有覆盖，需对取消状态下回滚也失败时的联合可观测性取证。 [core/lifecycle_group.go:178](../../../core/lifecycle_group.go#L178)
- [审查报告](../../../docs/evidence/stable-quality-review-dee7aa0f.md)

## 验收与验证

- [x] 断言 ctx 错误和 Stop 错误均可用 errors.Is 分类，下次 Stop 只重试待清理组件。
- [x] 运行以下命令并记录结果、失败路径和未验证范围。修改 Go 文件前完成仓库预检，验证前先执行仓库格式化。

```sh
go test -race -timeout 60s ./core
```

## 实施证据

[TestLifecycleGroupCancellationRollsBackAttemptedComponents](../../../core/lifecycle_group_test.go#L529)
