# Card 1708 — 测量深路径在对象池回收时的内存保留

Milestone: 稳定模块质量复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: router
Owned Files: `router/router_bench_test.go`
Depends On: 无

Goal:

测量深路径在对象池回收时的内存保留。

Scope:

对比边界前后 retain 值、回收成本及路由结果。

Non-goals:

本卡范围限于所列文件。维持公开签名和现有依赖，兼容契约调整先核对调用边界。

Files:

- `router/router_bench_test.go`

## Impact and Ownership

主模块为 `router`。根模块继续使用标准库。候审目标是复核可观察契约并记录证据。

## 证据

检查号为 29,34,35,48，状态为补证。

池已有 64 段上限，出池路径和 backtracking 的切片尾部清理相互影响。 [router/pool.go:47](https://github.com/spcent/plumego/blob/9430574179dc6b793524992833ce27496a5fe548/router/pool.go#L47)

[复审报告](../../../docs/evidence/stable-quality-review-94305741.md)

## Acceptance Tests

- `router/router_bench_test.go` 中的 `TestRouteStringPoolDiscardsDeepSlicesAndClearsTail`
- `router/router_bench_test.go` 中的 `BenchmarkPathPoolDeepPaths`

超出阈值不重新入池，较短路径切片尾部清零且分派一致。

Tests:

通过所列模块命令运行相邻用例并核对现有约定。

Docs Sync:

对照原契约核验，若证据要求修订公开行为，先更新实施范围。

## Validation

```sh
go test -race -timeout 60s ./router
go test ./router -run '^$' -bench '^BenchmarkPathPoolDeepPaths$' -benchmem -benchtime=200ms -count=3
```

Done Definition:

- [x] 超出阈值不重新入池，较短路径切片尾部清零且分派一致。
- [x] 执行验证命令，记录状态、首次失败和剩余核验范围。

Outcome:

超出容量上限的切片不入池，短切片的底层尾部清零。记录普通、边界和深路径的分配基准。

## 实施证据

- [TestRouteStringPoolDiscardsDeepSlicesAndClearsTail](../../../router/router_bench_test.go#L420)
- [BenchmarkPathPoolDeepPaths](../../../router/router_bench_test.go#L438)

[批次验证与基准](../review-94305741/README.md)
