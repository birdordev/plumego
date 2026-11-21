# Card 1740 — 验证空名称记录与统计占用的关系

Milestone: 稳定模块质量复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: metrics
Owned Files: `metrics/collector_test.go`
Depends On: 无

Goal:

验证空名称记录与统计占用的关系。

Scope:

混用命名、无名和错误事件，对照 Clear 后快照。

Non-goals:

本卡范围限于所列文件。维持公开签名和现有依赖，兼容契约调整先核对调用边界。

Files:

- `metrics/collector_test.go`

## Impact and Ownership

主模块为 `metrics`。根模块继续使用标准库。候审目标是复核可观察契约并记录证据。

## 证据

检查号为 6,14,32,44，状态为补证。

无名记录计入总量但不建立系列。已有普遍计数测试，不应把系列次数总和当成永远等于总量。 [metrics/collector.go:149](https://github.com/spcent/plumego/blob/9430574179dc6b793524992833ce27496a5fe548/metrics/collector.go#L149)

[复审报告](../../../docs/evidence/stable-quality-review-94305741.md)

## Acceptance Tests

- `metrics/collector_test.go` 中的 `TestUnnamedRecordsCountWithoutCreatingSeries`

总量精确且 active-series 仅含非空名称，快照独立。

Tests:

通过所列模块命令运行相邻用例并核对现有约定。

Docs Sync:

对照原契约核验，若证据要求修订公开行为，先更新实施范围。

## Validation

```sh
go test -race -timeout 60s ./metrics
```

Done Definition:

- [x] 总量精确且 active-series 仅含非空名称，快照独立。
- [x] 执行验证命令，记录状态、首次失败和剩余核验范围。

Outcome:

无名记录计入总量和适用的错误总量，不产生系列。快照 map 独立，Clear 后计数清零。

## 实施证据

- [TestUnnamedRecordsCountWithoutCreatingSeries](../../../metrics/collector_test.go#L242)

[批次验证与基准](../review-94305741/README.md)
