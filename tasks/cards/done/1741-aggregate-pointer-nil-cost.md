# Card 1741 — 度量 collector 构造时指针过滤与身份判断

Milestone: 稳定模块质量复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: metrics
Owned Files: `metrics/bench_test.go`
Depends On: 无

Goal:

度量 collector 构造时指针过滤与身份判断。

Scope:

加不同长度及 nil 比例的构造 benchmark。

Non-goals:

本卡范围限于所列文件。维持公开签名和现有依赖，兼容契约调整先核对调用边界。

Files:

- `metrics/bench_test.go`

## Impact and Ownership

主模块为 `metrics`。根模块继续使用标准库。候审目标是复核可观察契约并记录证据。

## 证据

检查号为 5,12,44,48，状态为补证。

构造对每个元素做 typed nil 指针识别，并保留单收集器的身份。 [metrics/multi.go:24](https://github.com/spcent/plumego/blob/9430574179dc6b793524992833ce27496a5fe548/metrics/multi.go#L24)

[复审报告](../../../docs/evidence/stable-quality-review-94305741.md)

## Acceptance Tests

- `metrics/bench_test.go` 中的 `BenchmarkMultiConstructionNilMix`
- `metrics/multi_test.go` 中的 `TestNewMultiCollectorFiltersNilPointers`

报告成本且不引入新依赖；有效收集器顺序稳定。

Tests:

通过所列模块命令运行相邻用例并核对现有约定。

Docs Sync:

对照原契约核验，若证据要求修订公开行为，先更新实施范围。

## Validation

```sh
go test -race -timeout 60s ./metrics
go test ./metrics -run '^$' -bench '^BenchmarkMultiConstructionNilMix$' -benchmem -benchtime=200ms -count=3
```

Done Definition:

- [x] 报告成本且不引入新依赖；有效收集器顺序稳定。
- [x] 执行验证命令，记录状态、首次失败和剩余核验范围。

Outcome:

测量不同元素数与 nil 混合比例的构造成本，复用原有的顺序和单目标身份验证。

## 实施证据

- [BenchmarkMultiConstructionNilMix](../../../metrics/bench_test.go#L101)
- [TestNewMultiCollectorFiltersNilPointers](../../../metrics/multi_test.go#L415)

[批次验证与基准](../review-94305741/README.md)
