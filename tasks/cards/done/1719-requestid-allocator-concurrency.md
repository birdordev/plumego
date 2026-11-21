# Card 1719 — 补证随机池回绕下的生成吞吐

Milestone: 稳定模块质量复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: middleware
Owned Files: `middleware/requestid/requestid_bench_test.go`
Depends On: 无

Goal:

补证随机池回绕下的生成吞吐。

Scope:

固定进程内生成器，比较单线程和并行的固定迭代。

Non-goals:

本卡范围限于所列文件。维持公开签名和现有依赖，兼容契约调整先核对调用边界。

Files:

- `middleware/requestid/requestid_bench_test.go`

## Impact and Ownership

主模块为 `middleware`。根模块继续使用标准库。候审目标是复核可观察契约并记录证据。

## 证据

检查号为 32,35,39,48，状态为补证。

上轮测试验证时间回拨。随机池更新在单写锁内，多 goroutine 的分配和停止条件需独立测量。 [middleware/requestid/generator.go:142](https://github.com/spcent/plumego/blob/9430574179dc6b793524992833ce27496a5fe548/middleware/requestid/generator.go#L142)

[复审报告](../../../docs/evidence/stable-quality-review-94305741.md)

## Acceptance Tests

- `middleware/requestid/requestid_bench_test.go` 中的 `TestParallelRequestIDsRemainUniqueAcrossPoolWrap`
- `middleware/requestid/requestid_bench_test.go` 中的 `BenchmarkRequestIDPoolParallel`

关联 ID 不重复，宽度稳定，报告分配和吞吐。

Tests:

通过所列模块命令运行相邻用例并核对现有约定。

Docs Sync:

对照原契约核验，若证据要求修订公开行为，先更新实施范围。

## Validation

```sh
go test -race -timeout 60s ./middleware/requestid
go test ./middleware/requestid -run '^$' -bench '^BenchmarkRequestIDPoolParallel$' -benchmem -benchtime=200ms -count=3
```

Done Definition:

- [x] 关联 ID 不重复，宽度稳定，报告分配和吞吐。
- [x] 执行验证命令，记录状态、首次失败和剩余核验范围。

Outcome:

并发生成跨过多个随机池周期，ID 宽度稳定且无重复。记录新生成器的并行吞吐与分配。

## 实施证据

- [TestParallelRequestIDsRemainUniqueAcrossPoolWrap](../../../middleware/requestid/requestid_bench_test.go#L45)
- [BenchmarkRequestIDPoolParallel](../../../middleware/requestid/requestid_bench_test.go#L71)

[批次验证与基准](../review-94305741/README.md)
