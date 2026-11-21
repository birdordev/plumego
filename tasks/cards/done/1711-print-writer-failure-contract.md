# Card 1711 — 补证 Print 首个写错误后停止输出

Milestone: 稳定模块质量复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P3
State: done
Primary Module: router
Owned Files: `router/router_print_test.go`
Depends On: 无

Goal:

补证 Print 首个写错误后停止输出。

Scope:

复用打印测试并注入写一次后失败的 writer。

Non-goals:

本卡范围限于所列文件。维持公开签名和现有依赖，兼容契约调整先核对调用边界。

Files:

- `router/router_print_test.go`

## Impact and Ownership

主模块为 `router`。根模块继续使用标准库。候审目标是复核可观察契约并记录证据。

## 证据

检查号为 16,22,29,37，状态为补证。

入口只接受 io.Writer，API 无返回值。文档注明遇到首错误停止，需要确认中途失败的输出边界。 [router/metadata.go:203](https://github.com/spcent/plumego/blob/9430574179dc6b793524992833ce27496a5fe548/router/metadata.go#L203)

[复审报告](../../../docs/evidence/stable-quality-review-94305741.md)

## Acceptance Tests

- `router/router_print_test.go` 中的 `TestRouterPrintStopsOnWriteError`

已写内容保留，后续路由不再尝试打印。

Tests:

通过所列模块命令运行相邻用例并核对现有约定。

Docs Sync:

对照原契约核验，若证据要求修订公开行为，先更新实施范围。

## Validation

```sh
go test -race -timeout 60s ./router
```

Done Definition:

- [x] 已写内容保留，后续路由不再尝试打印。
- [x] 执行验证命令，记录状态、首次失败和剩余核验范围。

Outcome:

在第 1、2 或 3 次写入拒绝时停止 Print，已成功写出的前缀完整保留。

## 实施证据

- [TestRouterPrintStopsOnWriteError](../../../router/router_print_test.go#L40)

[批次验证与基准](../review-94305741/README.md)
