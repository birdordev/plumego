# Card 1700 — 观测回调注释将路由覆盖写成整个下游请求

Milestone: 稳定模块质量复审
Recipe: specs/change-recipes/fix-bug.yaml
Context Package: implementation
Priority: P3
State: done
Primary Module: middleware
Owned Files: `middleware/internal/telemetry/helpers.go`, `middleware/internal/telemetry/helpers_test.go`
Depends On: 无

Goal:

观测回调注释将路由覆盖写成整个下游请求。

Scope:

明确复制边界及 span 从响应头等约定来源取得。

Non-goals:

本卡范围限于所列文件。维持公开签名和现有依赖，兼容契约调整先核对调用边界。

Files:

- `middleware/internal/telemetry/helpers.go`
- `middleware/internal/telemetry/helpers_test.go`

## Impact and Ownership

主模块为 `middleware`。根模块继续使用标准库。拟编辑范围与运行影响见本卡证据及验收。涉及安全与生命周期的变化按公开入口核验。

## 证据

检查号为 16,27,34,39，状态为源码确认。

回调收到入站准备后的请求，加上 writer 发布的路由元数据；注释可能让调用者以为下游任意 WithContext 值都回传。 [middleware/internal/telemetry/helpers.go:189](https://github.com/spcent/plumego/blob/9430574179dc6b793524992833ce27496a5fe548/middleware/internal/telemetry/helpers.go#L189)

[复审报告](../../../docs/evidence/stable-quality-review-94305741.md)

## Acceptance Tests

- `middleware/internal/telemetry/helpers_test.go` 中的 `TestWithObserverSharesRouteBetweenMetricsAndRequest`

核对旁路上下文不会传播，公开已知路由仍可读。

Tests:

通过所列模块命令运行相邻用例并核对现有约定。

Docs Sync:

对照原契约核验，若证据要求修订公开行为，先更新实施范围。

## Validation

```sh
go test -race -timeout 60s ./middleware/internal/telemetry
```

Done Definition:

- [x] 核对旁路上下文不会传播，公开已知路由仍可读。
- [x] 执行验证命令，记录状态、首次失败和剩余核验范围。

Outcome:

观测文档明确保留入站请求并合并 writer 发布的路由。下游仅本地派生的 Context 值不会传回，指标与请求路由保持一致。

## 实施证据

- [TestWithObserverSharesRouteBetweenMetricsAndRequest](../../../middleware/internal/telemetry/helpers_test.go#L163)

[批次验证与基准](../review-94305741/README.md)
