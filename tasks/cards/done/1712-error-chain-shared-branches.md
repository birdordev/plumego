# Card 1712 — 核对错误链多个分支的遍历优先级

Milestone: 稳定模块质量复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: contract
Owned Files: `contract/errors_chain_test.go`
Depends On: 无

Goal:

核对错误链多个分支的遍历优先级。

Scope:

用 acyclic join 与 wrapping 形成组合，验证优先级。

Non-goals:

本卡范围限于所列文件。维持公开签名和现有依赖，兼容契约调整先核对调用边界。

Files:

- `contract/errors_chain_test.go`

## Impact and Ownership

主模块为 `contract`。根模块继续使用标准库。候审目标是复核可观察契约并记录证据。

## 证据

检查号为 6,22,38，状态为补证。

已有 typed nil 检查，值优先于指针且按深度优先遍历。共享分支和多实例候选需额外证据。 [contract/errors.go:398](https://github.com/spcent/plumego/blob/9430574179dc6b793524992833ce27496a5fe548/contract/errors.go#L398)

[复审报告](../../../docs/evidence/stable-quality-review-94305741.md)

## Acceptance Tests

- `contract/errors_chain_test.go` 中的 `TestAsAPIErrorSharedBranchesChooseFirstValue`
- `contract/errors_chain_test.go` 中的 `TestAsAPIErrorSkipsTypedNilBranch`

返回第一个值匹配，其它状态和 request ID 照原例。

Tests:

通过所列模块命令运行相邻用例并核对现有约定。

Docs Sync:

对照原契约核验，若证据要求修订公开行为，先更新实施范围。

## Validation

```sh
go test -race -timeout 60s ./contract
```

Done Definition:

- [x] 返回第一个值匹配，其它状态和 request ID 照原例。
- [x] 执行验证命令，记录状态、首次失败和剩余核验范围。

Outcome:

同一分支多次出现仍保留深度优先顺序，并选中第一个值类型 APIError。

## 实施证据

- [TestAsAPIErrorSharedBranchesChooseFirstValue](../../../contract/errors_chain_test.go#L227)
- [TestAsAPIErrorSkipsTypedNilBranch](../../../contract/errors_chain_test.go#L215)

[批次验证与基准](../review-94305741/README.md)
