# Card 1715 — 补证错误 details 整数与空值的线格式

Milestone: 稳定模块质量复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: contract
Owned Files: `contract/response_test.go`
Depends On: 无

Goal:

补证错误 details 整数与空值的线格式。

Scope:

通过 WriteError 对照大整数、显式 null、空 map 和空 slice。

Non-goals:

本卡范围限于所列文件。维持公开签名和现有依赖，兼容契约调整先核对调用边界。

Files:

- `contract/response_test.go`

## Impact and Ownership

主模块为 `contract`。根模块继续使用标准库。候审目标是复核可观察契约并记录证据。

## 证据

检查号为 6,21,38,45，状态为补证。

响应错误字段包含 map，JSON 对整数及空容器有具体编码形状。 [contract/response.go:160](https://github.com/spcent/plumego/blob/9430574179dc6b793524992833ce27496a5fe548/contract/response.go#L160)

[复审报告](../../../docs/evidence/stable-quality-review-94305741.md)

## Acceptance Tests

- `contract/response_test.go` 中的 `TestWriteErrorPreservesIntegerDetailsAndEmptyContainers`

保留整数精度、已公布的空值形状及标准外壳。

Tests:

通过所列模块命令运行相邻用例并核对现有约定。

Docs Sync:

对照原契约核验，若证据要求修订公开行为，先更新实施范围。

## Validation

```sh
go test -race -timeout 60s ./contract
```

Done Definition:

- [x] 保留整数精度、已公布的空值形状及标准外壳。
- [x] 执行验证命令，记录状态、首次失败和剩余核验范围。

Outcome:

错误 details 保留高位整数、空数组和 null，request_id 保持在标准外壳。

## 实施证据

- [TestWriteErrorPreservesIntegerDetailsAndEmptyContainers](../../../contract/response_test.go#L771)

[批次验证与基准](../review-94305741/README.md)
