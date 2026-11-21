# Card 1694 — 正文限制覆写传入请求的 Body

Milestone: 稳定模块质量复审
Recipe: specs/change-recipes/fix-bug.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: middleware
Owned Files: `middleware/bodylimit/body_limit.go`, `middleware/bodylimit/body_limit_test.go`, `docs/modules/middleware/README.md`
Depends On: 无

Goal:

正文限制覆写传入请求的 Body。

Scope:

先核对 Close 及请求对象所有权，再通过浅复制请求把包装器传给下游，并检查外层超时取消。

Non-goals:

本卡范围限于所列文件。维持公开签名和现有依赖，兼容契约调整先核对调用边界。

Files:

- `middleware/bodylimit/body_limit.go`
- `middleware/bodylimit/body_limit_test.go`
- `docs/modules/middleware/README.md`

## Impact and Ownership

主模块为 `middleware`。根模块继续使用标准库。拟编辑范围与运行影响见本卡证据及验收。涉及安全与生命周期的变化按公开入口核验。

## 证据

检查号为 6,18,26,29,34，状态为源码确认。

正文限制在进入下游前改变同一个 Request。外层保留原请求的适配器在返回后读到包装器；与 requestid 已确定的派生请求所有权不同。 [middleware/bodylimit/body_limit.go:62](https://github.com/spcent/plumego/blob/9430574179dc6b793524992833ce27496a5fe548/middleware/bodylimit/body_limit.go#L62)

[复审报告](../../../docs/evidence/stable-quality-review-94305741.md)

## Acceptance Tests

- `middleware/bodylimit/body_limit_test.go` 中的 `TestAcceptanceBodyLimitBlockedWritesAndRequestOwnership`
- `middleware/bodylimit/body_limit_test.go` 中的 `TestBodyLimitRequestOwnershipAcrossTimeoutOrders`

原 Body 标识不变，下游读超限和 Close 正常；两种 timeout 顺序稳定。

Tests:

通过所列模块命令运行相邻用例并核对现有约定。

Docs Sync:

复核 `docs/modules/middleware/README.md`，模块说明改动后核对生成文件差异。

## Validation

```sh
go test -race -timeout 60s ./middleware/bodylimit
make website-sync
make website-check-generated
```

Done Definition:

- [x] 原 Body 标识不变，下游读超限和 Close 正常；两种 timeout 顺序稳定。
- [x] 执行验证命令，记录状态、首次失败和剩余核验范围。

Outcome:

只在派生请求安装包装 reader，保持父 Context、原 Body 身份和 Close 委托。正文限制与 timeout 的两种顺序都能完成一次 413。

## 实施证据

- [TestAcceptanceBodyLimitBlockedWritesAndRequestOwnership](../../../middleware/bodylimit/body_limit_test.go#L720)
- [TestBodyLimitRequestOwnershipAcrossTimeoutOrders](../../../middleware/bodylimit/body_limit_test.go#L780)

[批次验证与基准](../review-94305741/README.md)
