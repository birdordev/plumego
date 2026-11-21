# Card 1735 — 核对就绪快照时间和值语义

Milestone: 稳定模块质量复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: health
Owned Files: `health/json_contract_test.go`
Depends On: 无

Goal:

核对就绪快照时间和值语义。

Scope:

确认非 UTC 偏移时间在 JSON 和 Clone 中保留约定。

Non-goals:

本卡范围限于所列文件。维持公开签名和现有依赖，兼容契约调整先核对调用边界。

Files:

- `health/json_contract_test.go`

## Impact and Ownership

主模块为 `health`。根模块继续使用标准库。候审目标是复核可观察契约并记录证据。

## 证据

检查号为 6,16,34,42，状态为补证。

就绪快照由调用者写入时间，Clone 维护独立组件表及时间值。 [health/readiness.go:18](https://github.com/spcent/plumego/blob/9430574179dc6b793524992833ce27496a5fe548/health/readiness.go#L18)

[复审报告](../../../docs/evidence/stable-quality-review-94305741.md)

## Acceptance Tests

- `health/json_contract_test.go` 中的 `TestReadinessClonePreservesTimestampOffsetAndReason`

时间和 reason 未被克隆过程归一化，修改组件表仅影响克隆。

Tests:

通过所列模块命令运行相邻用例并核对现有约定。

Docs Sync:

对照原契约核验，若证据要求修订公开行为，先更新实施范围。

## Validation

```sh
go test -race -timeout 60s ./health
```

Done Definition:

- [x] 时间和 reason 未被克隆过程归一化，修改组件表仅影响克隆。
- [x] 执行验证命令，记录状态、首次失败和剩余核验范围。

Outcome:

保留非 UTC 时间偏移和原因文本。修改克隆的组件判定不会影响源对象。

## 实施证据

- [TestReadinessClonePreservesTimestampOffsetAndReason](../../../health/json_contract_test.go#L229)

[批次验证与基准](../review-94305741/README.md)
