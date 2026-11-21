# Card 1721 — 核对收尾 observe 对自定义回调的阻塞边界

Milestone: 稳定模块质量复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: middleware
Owned Files: `middleware/internal/telemetry/coverage_test.go`
Depends On: 无

Goal:

核对收尾 observe 对自定义回调的阻塞边界。

Scope:

用两个通道控制回调进入退出，对比时间和终态。

Non-goals:

本卡范围限于所列文件。维持公开签名和现有依赖，兼容契约调整先核对调用边界。

Files:

- `middleware/internal/telemetry/coverage_test.go`

## Impact and Ownership

主模块为 `middleware`。根模块继续使用标准库。候审目标是复核可观察契约并记录证据。

## 证据

检查号为 29,33,39,47，状态为补证。

回调同步运行且 panic 已隔离，耗时直接影响 ServeHTTP 返回。 [middleware/internal/telemetry/helpers.go:197](https://github.com/spcent/plumego/blob/9430574179dc6b793524992833ce27496a5fe548/middleware/internal/telemetry/helpers.go#L197)

[复审报告](../../../docs/evidence/stable-quality-review-94305741.md)

## Acceptance Tests

- `middleware/internal/telemetry/coverage_test.go` 中的 `TestObserverCompletesBeforeReturningAndIsolatesPanic`

准确记录返回时间与输出先后，普通传输只完成一次。

Tests:

通过所列模块命令运行相邻用例并核对现有约定。

Docs Sync:

对照原契约核验，若证据要求修订公开行为，先更新实施范围。

## Validation

```sh
go test -race -timeout 60s ./middleware/internal/telemetry
```

Done Definition:

- [x] 准确记录返回时间与输出先后，普通传输只完成一次。
- [x] 执行验证命令，记录状态、首次失败和剩余核验范围。

Outcome:

用通道证明观察者在 ServeHTTP 返回前同步执行，观察者 panic 被隔离，已提交响应保持。

## 实施证据

- [TestObserverCompletesBeforeReturningAndIsolatesPanic](../../../middleware/internal/telemetry/coverage_test.go#L403)

[批次验证与基准](../review-94305741/README.md)
