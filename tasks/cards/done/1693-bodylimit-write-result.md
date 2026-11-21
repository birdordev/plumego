# Card 1693 — 正文超限后被舍弃的写入返回成功

Milestone: 稳定模块质量复审
Recipe: specs/change-recipes/fix-bug.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: middleware
Owned Files: `middleware/bodylimit/body_limit.go`, `middleware/bodylimit/body_limit_test.go`, `docs/modules/middleware/README.md`
Depends On: 无

Goal:

正文超限后被舍弃的写入返回成功。

Scope:

盘点对阻断结果的依赖，再统一 Write 和 FlushError 的拒绝信号；原请求出现的 413 或已提交终态仍只写一次。

Non-goals:

本卡范围限于所列文件。维持公开签名和现有依赖，兼容契约调整先核对调用边界。

Files:

- `middleware/bodylimit/body_limit.go`
- `middleware/bodylimit/body_limit_test.go`
- `docs/modules/middleware/README.md`

## Impact and Ownership

主模块为 `middleware`。根模块继续使用标准库。拟编辑范围与运行影响见本卡证据及验收。涉及安全与生命周期的变化按公开入口核验。

## 证据

检查号为 6,20,22,23,39，状态为已复现。

超限产生一次 413 且下游正文被舍弃；下游 Write 的返回却为完整字节数、nil 错误。业务可能误以为已发送，和同一包装的 FlushError 不一致。 [middleware/bodylimit/body_limit.go:107](https://github.com/spcent/plumego/blob/9430574179dc6b793524992833ce27496a5fe548/middleware/bodylimit/body_limit.go#L107)

[复审报告](../../../docs/evidence/stable-quality-review-94305741.md)

## Acceptance Tests

- `middleware/bodylimit/body_limit_test.go` 中的 `TestAcceptanceBodyLimitBlockedWritesAndRequestOwnership`
- `middleware/bodylimit/body_limit_test.go` 中的 `TestMiddlewareDownstreamWriteAfterOverrunReportsRejection`

同时验证提交前、提交后、空写入、写入数与错误分类。

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

- [x] 同时验证提交前、提交后、空写入、写入数与错误分类。
- [x] 执行验证命令，记录状态、首次失败和剩余核验范围。

Outcome:

盘点阻断调用后更新既有成功计数约定。被阻断的空写与普通写均返回 0 和正文限制错误，Flush 报同类错误。已提交错误仍可完成正文。

## 实施证据

- [TestAcceptanceBodyLimitBlockedWritesAndRequestOwnership](../../../middleware/bodylimit/body_limit_test.go#L720)
- [TestMiddlewareDownstreamWriteAfterOverrunReportsRejection](../../../middleware/bodylimit/body_limit_test.go#L163)

[批次验证与基准](../review-94305741/README.md)
