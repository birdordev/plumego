# Card 1695 — 回放超限后缓冲容量仍被保留

Milestone: 稳定模块质量复审
Recipe: specs/change-recipes/fix-bug.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: middleware
Owned Files: `internal/httputil/buffered_response.go`, `middleware/timeout/timeout.go`, `middleware/timeout/timeout_regression_test.go`, `docs/modules/middleware/README.md`
Depends On: 无

Goal:

回放超限后缓冲容量仍被保留。

Scope:

在确认回放放弃后释放正文底层存储，同时保留已捕获的响应状态和退出路径；同步修正文档。

Non-goals:

本卡范围限于所列文件。维持公开签名和现有依赖，兼容契约调整先核对调用边界。

Files:

- `internal/httputil/buffered_response.go`
- `middleware/timeout/timeout.go`
- `middleware/timeout/timeout_regression_test.go`
- `docs/modules/middleware/README.md`

## Impact and Ownership

主模块为 `middleware`。根模块继续使用标准库。拟编辑范围与运行影响见本卡证据及验收。涉及安全与生命周期的变化按公开入口核验。

## 证据

检查号为 16,23,29,35,48，状态为源码确认。

ClearBody 调用 bytes.Buffer.Reset，复用容量保留至 recorder 不可达。原注释说释放内存，迟到 worker 仍可持有对象。 [middleware/timeout/timeout.go:268](https://github.com/spcent/plumego/blob/9430574179dc6b793524992833ce27496a5fe548/middleware/timeout/timeout.go#L268)

[复审报告](../../../docs/evidence/stable-quality-review-94305741.md)

## Acceptance Tests

- `middleware/timeout/timeout_regression_test.go` 中的 `TestAcceptanceTimeoutReplayReleasesCapturedBuffer`
- `internal/httputil/transport_test.go` 中的 `TestBufferedResponse_ClearBody`

注入大而有界正文超限，检查保留容量、退出及单次错误提交，不观察不相关 GC 波动。

Tests:

通过所列模块命令运行相邻用例，同时运行 internal/httputil 的相应 recorder 回归并核对现有约定。

Docs Sync:

复核 `docs/modules/middleware/README.md`，模块说明改动后核对生成文件差异。

## Validation

```sh
go test -race -timeout 60s ./middleware/timeout ./internal/httputil
make website-sync
make website-check-generated
```

Done Definition:

- [x] 注入大而有界正文超限，检查保留容量、退出及单次错误提交，不观察不相关 GC 波动。
- [x] 执行验证命令，记录状态、首次失败和剩余核验范围。

Outcome:

超出回放预算后清空 recorder 持有的底层内存，保留原选状态供内部判断，客户端收到单次规范 500。共享 recorder 和压缩相关回归已运行。

## 实施证据

- [TestAcceptanceTimeoutReplayReleasesCapturedBuffer](../../../middleware/timeout/timeout_regression_test.go#L434)
- [TestBufferedResponse_ClearBody](../../../internal/httputil/transport_test.go#L396)

[批次验证与基准](../review-94305741/README.md)
