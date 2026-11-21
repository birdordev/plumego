# Card 1717 — 核对恢复层在 transport Flush 失败后的终态权

Milestone: 稳定模块质量复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: middleware
Owned Files: `middleware/conformance/response_writer_contract_test.go`
Depends On: 无

Goal:

核对恢复层在 transport Flush 失败后的终态权。

Scope:

在 controlled writer 配置两类非安全 I/O 错误，随后受控 panic。

Non-goals:

本卡范围限于所列文件。维持公开签名和现有依赖，兼容契约调整先核对调用边界。

Files:

- `middleware/conformance/response_writer_contract_test.go`

## Impact and Ownership

主模块为 `middleware`。根模块继续使用标准库。候审目标是复核可观察契约并记录证据。

## 证据

检查号为 23,24,39,46，状态为补证。

目前按 wrapped writer 的 Written 决定是否生成错误；Flush 的普通 I/O 错误和不支持有不同语义。 [middleware/recovery/recover.go:69](https://github.com/spcent/plumego/blob/9430574179dc6b793524992833ce27496a5fe548/middleware/recovery/recover.go#L69)

[复审报告](../../../docs/evidence/stable-quality-review-94305741.md)

## Acceptance Tests

- `middleware/conformance/response_writer_contract_test.go` 中的 `TestRecoveryAfterFlushFailureRespectsCommitOwnership`

无二次终态；未提交者写统一 500，已提交者保留。

Tests:

通过所列模块命令运行相邻用例并核对现有约定。

Docs Sync:

对照原契约核验，若证据要求修订公开行为，先更新实施范围。

## Validation

```sh
go test -race -timeout 60s ./middleware/conformance
```

Done Definition:

- [x] 无二次终态；未提交者写统一 500，已提交者保留。
- [x] 执行验证命令，记录状态、首次失败和剩余核验范围。

Outcome:

不支持 Flush 时恢复可以写 500。底层已提交并返回普通 I/O 错误时保留首次提交。

## 实施证据

- [TestRecoveryAfterFlushFailureRespectsCommitOwnership](../../../middleware/conformance/response_writer_contract_test.go#L491)

[批次验证与基准](../review-94305741/README.md)
