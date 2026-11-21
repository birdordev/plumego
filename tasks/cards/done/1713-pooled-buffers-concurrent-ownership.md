# Card 1713 — 核对 JSON 池在慢写与早退时的归属

Milestone: 稳定模块质量复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: contract
Owned Files: `contract/response_test.go`
Depends On: 无

Goal:

核对 JSON 池在慢写与早退时的归属。

Scope:

阻塞一个合法 writer，另一 goroutine 输出不同内容。

Non-goals:

本卡范围限于所列文件。维持公开签名和现有依赖，兼容契约调整先核对调用边界。

Files:

- `contract/response_test.go`

## Impact and Ownership

主模块为 `contract`。根模块继续使用标准库。候审目标是复核可观察契约并记录证据。

## 证据

检查号为 23,29,32,34,38，状态为补证。

池容量已有明确上限。慢 writer 持有切片直到 Write 返回，池归还发生于 defer。 [contract/response_buffer.go:20](https://github.com/spcent/plumego/blob/9430574179dc6b793524992833ce27496a5fe548/contract/response_buffer.go#L20)

[复审报告](../../../docs/evidence/stable-quality-review-94305741.md)

## Acceptance Tests

- `contract/response_test.go` 中的 `TestPooledJSONBufferRemainsOwnedUntilWriteReturns`
- `contract/response_test.go` 中的 `TestPutJSONBufferRetainsOnlyBoundedCapacity`

两个响应各自完整，超大 buffer 不重新进池。

Tests:

通过所列模块命令运行相邻用例并核对现有约定。

Docs Sync:

对照原契约核验，若证据要求修订公开行为，先更新实施范围。

## Validation

```sh
go test -race -timeout 60s ./contract
```

Done Definition:

- [x] 两个响应各自完整，超大 buffer 不重新进池。
- [x] 执行验证命令，记录状态、首次失败和剩余核验范围。

Outcome:

慢 writer 持有正文时并发响应保持独立。现有容量用例确认超大缓冲不进入复用池。

## 实施证据

- [TestPooledJSONBufferRemainsOwnedUntilWriteReturns](../../../contract/response_test.go#L755)
- [TestPutJSONBufferRetainsOnlyBoundedCapacity](../../../contract/response_test.go#L15)

[批次验证与基准](../review-94305741/README.md)
