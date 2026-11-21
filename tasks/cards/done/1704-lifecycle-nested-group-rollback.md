# Card 1704 — 核对嵌套 LifecycleGroup 回滚后的保留项

Milestone: 稳定模块质量复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: core
Owned Files: `core/lifecycle_group_test.go`
Depends On: 无

Goal:

核对嵌套 LifecycleGroup 回滚后的保留项。

Scope:

构造两层组和一次子清理失败，不修改公开方法。

Non-goals:

本卡范围限于所列文件。维持公开签名和现有依赖，兼容契约调整先核对调用边界。

Files:

- `core/lifecycle_group_test.go`

## Impact and Ownership

主模块为 `core`。根模块继续使用标准库。候审目标是复核可观察契约并记录证据。

## 证据

检查号为 28,29,30,36,46，状态为补证。

子 Group 自行回滚，父 Group 仍可能再次 Stop 该子项。需要联合验证重试边界和错误链。 [core/lifecycle_group.go:178](https://github.com/spcent/plumego/blob/9430574179dc6b793524992833ce27496a5fe548/core/lifecycle_group.go#L178)

[复审报告](../../../docs/evidence/stable-quality-review-94305741.md)

## Acceptance Tests

- `core/lifecycle_group_test.go` 中的 `TestNestedGroupRetriesOnlyPendingChildCleanup`

原始错误保留、成功项只清理一次、失败项按约定重试。

Tests:

通过所列模块命令运行相邻用例并核对现有约定。

Docs Sync:

对照原契约核验，若证据要求修订公开行为，先更新实施范围。

## Validation

```sh
go test -race -timeout 60s ./core
```

Done Definition:

- [x] 原始错误保留、成功项只清理一次、失败项按约定重试。
- [x] 执行验证命令，记录状态、首次失败和剩余核验范围。

Outcome:

保留嵌套组的主错误及清理错误。父组回滚只重试子组的失败项，后续 Stop 继续清理同一项。

## 实施证据

- [TestNestedGroupRetriesOnlyPendingChildCleanup](../../../core/lifecycle_group_test.go#L676)

[批次验证与基准](../review-94305741/README.md)
