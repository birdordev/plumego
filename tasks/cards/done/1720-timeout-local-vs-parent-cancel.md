# Card 1720 — 补证本地期限与上游取消的接近判决

Milestone: 稳定模块质量复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: middleware
Owned Files: `middleware/timeout/timeout_regression_test.go`
Depends On: 无

Goal:

补证本地期限与上游取消的接近判决。

Scope:

用合成时间和明确事件顺序验证期限发生前后。

Non-goals:

本卡范围限于所列文件。维持公开签名和现有依赖，兼容契约调整先核对调用边界。

Files:

- `middleware/timeout/timeout_regression_test.go`

## Impact and Ownership

主模块为 `middleware`。根模块继续使用标准库。候审目标是复核可观察契约并记录证据。

## 证据

检查号为 23,26,32,39,47，状态为补证。

已有父期限、迟到 worker 和单项取消用例。组合两类事件时只用标准 context 判定，不宜凭调度顺序。 [middleware/timeout/timeout.go:147](https://github.com/spcent/plumego/blob/9430574179dc6b793524992833ce27496a5fe548/middleware/timeout/timeout.go#L147)

[复审报告](../../../docs/evidence/stable-quality-review-94305741.md)

## Acceptance Tests

- `middleware/timeout/timeout_regression_test.go` 中的 `TestTimeoutCloseParentCancellationAndLocalDeadline`

父取消无合成 504，实际 DeadlineExceeded 只提交一次。

Tests:

通过所列模块命令运行相邻用例并核对现有约定。

Docs Sync:

对照原契约核验，若证据要求修订公开行为，先更新实施范围。

## Validation

```sh
go test -race -timeout 60s ./middleware/timeout
```

Done Definition:

- [x] 父取消无合成 504，实际 DeadlineExceeded 只提交一次。
- [x] 执行验证命令，记录状态、首次失败和剩余核验范围。

Outcome:

使用合成时钟核对父取消先发生与本地期限先到，分别观察零提交与单次 504。

## 实施证据

- [TestTimeoutCloseParentCancellationAndLocalDeadline](../../../middleware/timeout/timeout_regression_test.go#L455)

[批次验证与基准](../review-94305741/README.md)
