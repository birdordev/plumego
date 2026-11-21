# Card 1578 — 生命周期清理回调异常退出时中断逆序清理

Milestone: 本轮质量审查
Recipe: specs/change-recipes/fix-bug.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: core
Owned Files: `core/lifecycle_group.go`, `core/lifecycle_group_test.go`, `docs/modules/core/README.md`, `website/src/generated/modules.ts`
Depends On: 无

Goal: 生命周期清理回调异常退出时中断逆序清理。
Scope: 先明确非 panic 异常退出的支持边界及重试语义，按决定记录并验证待清理标记。
Non-goals: 保留现有公开签名和依赖。涉及未授权的行为或兼容契约调整时，先完成调用方核对与兼容决策。
Files: `core/lifecycle_group.go`, `core/lifecycle_group_test.go`, `docs/modules/core/README.md`, `website/src/generated/modules.ts`。
Tests: 隔离 goroutine 验证回调终止后组阶段和待清理项，后续 Stop 只处理未完成项。
Docs Sync: 核对 `docs/modules/core/README.md`。 修改模块说明后检查网站生成差异，并在同一交付中保留相应生成文件。
Done Definition: 完成下列验收和验证，记录证据与剩余风险。
Outcome: 明确回调终止调用 goroutine 会中断当轮清理，验证重试仅重做失败或尚未执行项。

## 证据与影响

- 检查号：28,29,30,36。证据状态：源码确认。
- 回调直接终止当前 goroutine 时 recover 无异常值，stopPending 循环随栈退出，后续待清理项要靠另一次 Stop。当前文档只精确处理 panic 与错误。 [core/lifecycle_group.go:265](../../../core/lifecycle_group.go#L265)
- [审查报告](../../../docs/evidence/stable-quality-review-dee7aa0f.md)

## 验收与验证

- [x] 隔离 goroutine 验证回调终止后组阶段和待清理项，后续 Stop 只处理未完成项。
- [x] 运行以下命令并记录结果、失败路径和未验证范围。修改 Go 文件前完成仓库预检，验证前先执行仓库格式化。

```sh
go test -race -timeout 60s ./core
make website-sync
make website-check-generated
```

## 实施证据

[TestLifecycleGroupRetainsPendingStopAfterGoexit](../../../core/lifecycle_group_test.go#L611)
