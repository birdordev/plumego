# Card 1707 — 锁定冻结对已有 group 视图的共享作用

Milestone: 稳定模块质量复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: router
Owned Files: `router/freeze_test.go`
Depends On: 无

Goal:

锁定冻结对已有 group 视图的共享作用。

Scope:

创建两个已存在 group、从其中一个冻结，然后从根及另一个调用注册和 URL。

Non-goals:

本卡范围限于所列文件。维持公开签名和现有依赖，兼容契约调整先核对调用边界。

Files:

- `router/freeze_test.go`

## Impact and Ownership

主模块为 `router`。根模块继续使用标准库。候审目标是复核可观察契约并记录证据。

## 证据

检查号为 28,31,32,37，状态为补证。

group 名下共享根状态，冻结的双向可见性可能影响已登记的 group 句柄。 [router/router.go:213](https://github.com/spcent/plumego/blob/9430574179dc6b793524992833ce27496a5fe548/router/router.go#L213)

[复审报告](../../../docs/evidence/stable-quality-review-94305741.md)

## Acceptance Tests

- `router/freeze_test.go` 中的 `TestFreezeThroughGroupClosesExistingRegistrationViews`

所有视图拒绝新注册，已有命名和元数据稳定。

Tests:

通过所列模块命令运行相邻用例并核对现有约定。

Docs Sync:

对照原契约核验，若证据要求修订公开行为，先更新实施范围。

## Validation

```sh
go test -race -timeout 60s ./router
```

Done Definition:

- [x] 所有视图拒绝新注册，已有命名和元数据稳定。
- [x] 执行验证命令，记录状态、首次失败和剩余核验范围。

Outcome:

从已有 group 冻结会锁住所有视图的新增注册，旧命名 URL 与全局路由元数据稳定。

## 实施证据

- [TestFreezeThroughGroupClosesExistingRegistrationViews](../../../router/freeze_test.go#L100)

[批次验证与基准](../review-94305741/README.md)
