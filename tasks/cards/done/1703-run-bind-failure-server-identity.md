# Card 1703 — 验证 Run 监听失败后服务器句柄的复用

Milestone: 稳定模块质量复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: core
Owned Files: `core/lifecycle_test.go`
Depends On: 无

Goal:

验证 Run 监听失败后服务器句柄的复用。

Scope:

用保留中的本地 listener 触发失败后释放，再测试复用及终点。

Non-goals:

本卡范围限于所列文件。维持公开签名和现有依赖，兼容契约调整先核对调用边界。

Files:

- `core/lifecycle_test.go`

## Impact and Ownership

主模块为 `core`。根模块继续使用标准库。候审目标是复核可观察契约并记录证据。

## 证据

检查号为 6,28,31,36，状态为补证。

Run 在绑定之前已经发布 Server；绑定失败并不等于完成 Shutdown。 [core/lifecycle.go:34](https://github.com/spcent/plumego/blob/9430574179dc6b793524992833ce27496a5fe548/core/lifecycle.go#L34)

[复审报告](../../../docs/evidence/stable-quality-review-94305741.md)

## Acceptance Tests

- `core/lifecycle_test.go` 中的 `TestRunBindFailureKeepsPreparedServerForRetry`

返回可分类监听错误，同一 Server 的应用仍可依法重试。

Tests:

通过所列模块命令运行相邻用例并核对现有约定。

Docs Sync:

对照原契约核验，若证据要求修订公开行为，先更新实施范围。

## Validation

```sh
go test -race -timeout 60s ./core
```

Done Definition:

- [x] 返回可分类监听错误，同一 Server 的应用仍可依法重试。
- [x] 执行验证命令，记录状态、首次失败和剩余核验范围。

Outcome:

Run 监听失败保留可识别的 net.OpError，地址释放后复用相同 Server 启动并退出。

## 实施证据

- [TestRunBindFailureKeepsPreparedServerForRetry](../../../core/lifecycle_test.go#L1092)

[批次验证与基准](../review-94305741/README.md)
