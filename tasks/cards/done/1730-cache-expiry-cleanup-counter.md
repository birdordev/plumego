# Card 1730 — 核对后台清理与覆盖时的待清理判据

Milestone: 稳定模块质量复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: store
Owned Files: `store/cache/state_regression_test.go`
Depends On: 无

Goal:

核对后台清理与覆盖时的待清理判据。

Scope:

固定时钟并为清理和 Set 建立通道顺序。

Non-goals:

本卡范围限于所列文件。维持公开签名和现有依赖，兼容契约调整先核对调用边界。

Files:

- `store/cache/state_regression_test.go`

## Impact and Ownership

主模块为 `store`。根模块继续使用标准库。候审目标是复核可观察契约并记录证据。

## 证据

检查号为 28,31,32,41,47，状态为补证。

过期扫描拿旧项，进锁后比较当前值和有效期。并发覆盖必须避免旧扫描误删新项。 [store/cache/memory.go:116](https://github.com/spcent/plumego/blob/9430574179dc6b793524992833ce27496a5fe548/store/cache/memory.go#L116)

[复审报告](../../../docs/evidence/stable-quality-review-94305741.md)

## Acceptance Tests

- `store/cache/state_regression_test.go` 中的 `TestStaleCleanupScanPreservesReplacementAndAccounting`

新项可 Get、大小计数精确、后续真实过期可删除。

Tests:

通过所列模块命令运行相邻用例并核对现有约定。

Docs Sync:

对照原契约核验，若证据要求修订公开行为，先更新实施范围。

## Validation

```sh
go test -race -timeout 60s ./store/cache
```

Done Definition:

- [x] 新项可 Get、大小计数精确、后续真实过期可删除。
- [x] 执行验证命令，记录状态、首次失败和剩余核验范围。

Outcome:

让过期扫描先取得旧值，再覆盖并恢复清理。新值、大小及 payload 计数正确，真正过期后清理归零。

## 实施证据

- [TestStaleCleanupScanPreservesReplacementAndAccounting](../../../store/cache/state_regression_test.go#L294)

[批次验证与基准](../review-94305741/README.md)
