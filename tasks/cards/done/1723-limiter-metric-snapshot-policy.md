# Card 1723 — 核对 limiter 独立统计字段与稳定状态

Milestone: 稳定模块质量复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: security
Owned Files: `security/abuse/limiter_test.go`
Depends On: 无

Goal:

核对 limiter 独立统计字段与稳定状态。

Scope:

交错新键和清理，先锁定独立快照契约，再验证静止后的总计。

Non-goals:

本卡范围限于所列文件。维持公开签名和现有依赖，兼容契约调整先核对调用边界。

Files:

- `security/abuse/limiter_test.go`

## Impact and Ownership

主模块为 `security`。根模块继续使用标准库。候审目标是复核可观察契约并记录证据。

## 证据

检查号为 32,34,40,47，状态为补证。

统计为多个独立原子 load，Buckets 和 Evictions 可能来自不同瞬间。 [security/abuse/limiter.go:537](https://github.com/spcent/plumego/blob/9430574179dc6b793524992833ce27496a5fe548/security/abuse/limiter.go#L537)

[复审报告](../../../docs/evidence/stable-quality-review-94305741.md)

## Acceptance Tests

- `security/abuse/limiter_test.go` 中的 `TestLimiterMetricsIndividualBoundsAndSettledTotals`

稳定后总数准确，运行时单字段始终满足自身范围。

Tests:

通过所列模块命令运行相邻用例并核对现有约定。

Docs Sync:

对照原契约核验，若证据要求修订公开行为，先更新实施范围。

## Validation

```sh
go test -race -timeout 60s ./security/abuse
```

Done Definition:

- [x] 稳定后总数准确，运行时单字段始终满足自身范围。
- [x] 执行验证命令，记录状态、首次失败和剩余核验范围。

Outcome:

更新途中只断言各计数器自身范围；完成后检查拒绝、通过、驱逐和活动桶精确值。

## 实施证据

- [TestLimiterMetricsIndividualBoundsAndSettledTotals](../../../security/abuse/limiter_test.go#L910)

[批次验证与基准](../review-94305741/README.md)
