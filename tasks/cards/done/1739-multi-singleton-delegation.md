# Card 1739 — 核对单目标 fast path 的字段所有权

Milestone: 稳定模块质量复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: metrics
Owned Files: `metrics/multi_test.go`
Depends On: 无

Goal:

核对单目标 fast path 的字段所有权。

Scope:

用 test sink 对照 nil、单目标和双目标，记录承诺范围。

Non-goals:

本卡范围限于所列文件。维持公开签名和现有依赖，兼容契约调整先核对调用边界。

Files:

- `metrics/multi_test.go`

## Impact and Ownership

主模块为 `metrics`。根模块继续使用标准库。候审目标是复核可观察契约并记录证据。

## 证据

检查号为 6,13,34,44，状态为补证。

单目标不包裹并直接返回，有别于多目标复制。自定义子收集器可持有它收到的 labels，所以构造的返回语义应可验证。 [metrics/multi.go:42](https://github.com/spcent/plumego/blob/9430574179dc6b793524992833ce27496a5fe548/metrics/multi.go#L42)

[复审报告](../../../docs/evidence/stable-quality-review-94305741.md)

## Acceptance Tests

- `metrics/multi_test.go` 中的 `TestNewMultiCollectorNilFilteringAndSinglePassthrough`
- `metrics/multi_test.go` 中的 `TestNewMultiCollectorFiltersNilPointers`
- `metrics/multi_test.go` 中的 `TestMultiCollectorPreservesEmptyLabelShape`

单目标身份和正常 API 维持，多目标隔离。

Tests:

通过所列模块命令运行相邻用例并核对现有约定。

Docs Sync:

对照原契约核验，若证据要求修订公开行为，先更新实施范围。

## Validation

```sh
go test -race -timeout 60s ./metrics
```

Done Definition:

- [x] 单目标身份和正常 API 维持，多目标隔离。
- [x] 执行验证命令，记录状态、首次失败和剩余核验范围。

Outcome:

复用单目标身份与多目标复制验证，确认过滤 typed nil 后维持顺序，各子收集器接收独立 labels。

## 实施证据

- [TestNewMultiCollectorNilFilteringAndSinglePassthrough](../../../metrics/multi_test.go#L300)
- [TestNewMultiCollectorFiltersNilPointers](../../../metrics/multi_test.go#L415)
- [TestMultiCollectorPreservesEmptyLabelShape](../../../metrics/multi_test.go#L387)

[批次验证与基准](../review-94305741/README.md)
