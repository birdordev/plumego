# Card 1714 — 核对 request ID 与 RouteState 共存时的快照

Milestone: 稳定模块质量复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: contract
Owned Files: `contract/context_test.go`
Depends On: 无

Goal:

核对 request ID 与 RouteState 共存时的快照。

Scope:

组合 RequestContext、请求 ID 和 RouteState，核查只读操作。

Non-goals:

本卡范围限于所列文件。维持公开签名和现有依赖，兼容契约调整先核对调用边界。

Files:

- `contract/context_test.go`

## Impact and Ownership

主模块为 `contract`。根模块继续使用标准库。候审目标是复核可观察契约并记录证据。

## 证据

检查号为 27,34,38，状态为补证。

多个载体用不同 context key，参数快照可能在层叠调用后读取。 [contract/context_core.go:114](https://github.com/spcent/plumego/blob/9430574179dc6b793524992833ce27496a5fe548/contract/context_core.go#L114)

[复审报告](../../../docs/evidence/stable-quality-review-94305741.md)

## Acceptance Tests

- `contract/context_test.go` 中的 `TestNestedRequestMetadataKeepsIndependentCarriers`

请求 ID 与 metadata 相互独立，优先级和原 map 所有权保持。

Tests:

通过所列模块命令运行相邻用例并核对现有约定。

Docs Sync:

对照原契约核验，若证据要求修订公开行为，先更新实施范围。

## Validation

```sh
go test -race -timeout 60s ./contract
```

Done Definition:

- [x] 请求 ID 与 metadata 相互独立，优先级和原 map 所有权保持。
- [x] 执行验证命令，记录状态、首次失败和剩余核验范围。

Outcome:

请求 ID、父元数据和派生 RouteState 搭配时，路由参数优先，源 map 修改不影响已写入的元数据。

## 实施证据

- [TestNestedRequestMetadataKeepsIndependentCarriers](../../../contract/context_test.go#L184)

[批次验证与基准](../review-94305741/README.md)
