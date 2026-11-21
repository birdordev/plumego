# Card 1705 — 核对 core 包装与 router 注册错误的分类稳定性

Milestone: 稳定模块质量复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: core
Owned Files: `core/routing_test.go`
Depends On: 无

Goal:

核对 core 包装与 router 注册错误的分类稳定性。

Scope:

并列核对 nil、冻结及重复命名，锁定调用者能依赖的分类。

Non-goals:

本卡范围限于所列文件。维持公开签名和现有依赖，兼容契约调整先核对调用边界。

Files:

- `core/routing_test.go`

## Impact and Ownership

主模块为 `core`。根模块继续使用标准库。候审目标是复核可观察契约并记录证据。

## 证据

检查号为 19,22,36，状态为补证。

App 状态拒绝有 core 操作标签，router 层结果直接返回。 [core/routing.go:31](https://github.com/spcent/plumego/blob/9430574179dc6b793524992833ce27496a5fe548/core/routing.go#L31)

[复审报告](../../../docs/evidence/stable-quality-review-94305741.md)

## Acceptance Tests

- `core/routing_test.go` 中的 `TestAddRouteErrorOwnershipAndFrozenPrecedence`

原始类型和返回顺序清楚，避免响应或日志直接使用任意路径文本。

Tests:

通过所列模块命令运行相邻用例并核对现有约定。

Docs Sync:

对照原契约核验，若证据要求修订公开行为，先更新实施范围。

## Validation

```sh
go test -race -timeout 60s ./core
```

Done Definition:

- [x] 原始类型和返回顺序清楚，避免响应或日志直接使用任意路径文本。
- [x] 执行验证命令，记录状态、首次失败和剩余核验范围。

Outcome:

nil handler 原因可分类，重复命名来自 router，冻结拒绝优先于 handler 验证。

## 实施证据

- [TestAddRouteErrorOwnershipAndFrozenPrecedence](../../../core/routing_test.go#L620)

[批次验证与基准](../review-94305741/README.md)
