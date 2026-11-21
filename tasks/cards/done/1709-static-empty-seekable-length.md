# Card 1709 — 核对静态空文件与显式媒体类型

Milestone: 稳定模块质量复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: router
Owned Files: `router/static_test.go`
Depends On: 无

Goal:

核对静态空文件与显式媒体类型。

Scope:

对照两个 file fixture 和显式 header。

Non-goals:

本卡范围限于所列文件。维持公开签名和现有依赖，兼容契约调整先核对调用边界。

Files:

- `router/static_test.go`

## Impact and Ownership

主模块为 `router`。根模块继续使用标准库。候审目标是复核可观察契约并记录证据。

## 证据

检查号为 6,24,37，状态为补证。

标准 seekable 与回退转发都涉及未知或零大小、媒体类型及 HEAD。 [router/static.go:278](https://github.com/spcent/plumego/blob/9430574179dc6b793524992833ce27496a5fe548/router/static.go#L278)

[复审报告](../../../docs/evidence/stable-quality-review-94305741.md)

## Acceptance Tests

- `router/static_test.go` 中的 `TestStaticFSEmptySeekableFileHasZeroLength`
- `router/static_test.go` 中的 `TestStaticFSSequentialAndSeekable`

GET、HEAD 均不读取空正文到响应，已知零长度与状态一致。

Tests:

通过所列模块命令运行相邻用例并核对现有约定。

Docs Sync:

对照原契约核验，若证据要求修订公开行为，先更新实施范围。

## Validation

```sh
go test -race -timeout 60s ./router
```

Done Definition:

- [x] GET、HEAD 均不读取空正文到响应，已知零长度与状态一致。
- [x] 执行验证命令，记录状态、首次失败和剩余核验范围。

Outcome:

seekable 空文件的 GET 和 HEAD 保留显式媒体类型及 Content-Length: 0，正文为空。

## 实施证据

- [TestStaticFSEmptySeekableFileHasZeroLength](../../../router/static_test.go#L630)
- [TestStaticFSSequentialAndSeekable](../../../router/static_test.go#L510)

[批次验证与基准](../review-94305741/README.md)
