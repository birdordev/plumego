# Card 1710 — 覆盖 Allow 对路径归一化与冻结结果的一致性

Milestone: 稳定模块质量复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: router
Owned Files: `router/router_contract_test.go`
Depends On: 无

Goal:

覆盖 Allow 对路径归一化与冻结结果的一致性。

Scope:

比较有尾斜线、重复前导斜线及空路径，控制出口冷暖。

Non-goals:

本卡范围限于所列文件。维持公开签名和现有依赖，兼容契约调整先核对调用边界。

Files:

- `router/router_contract_test.go`

## Impact and Ownership

主模块为 `router`。根模块继续使用标准库。候审目标是复核可观察契约并记录证据。

## 证据

检查号为 18,32,37,45，状态为补证。

匹配和 Allow 查询路径都经过归一化，先前验证重点为普通路径。 [router/dispatch.go:184](https://github.com/spcent/plumego/blob/9430574179dc6b793524992833ce27496a5fe548/router/dispatch.go#L184)

[复审报告](../../../docs/evidence/stable-quality-review-94305741.md)

## Acceptance Tests

- `router/router_contract_test.go` 中的 `TestRoutingMatrixBeforeAndAfterFreeze`

HEAD 隐含在 GET 后可用，404/405 与 Allow 一致。

Tests:

通过所列模块命令运行相邻用例并核对现有约定。

Docs Sync:

对照原契约核验，若证据要求修订公开行为，先更新实施范围。

## Validation

```sh
go test -race -timeout 60s ./router
```

Done Definition:

- [x] HEAD 隐含在 GET 后可用，404/405 与 Allow 一致。
- [x] 执行验证命令，记录状态、首次失败和剩余核验范围。

Outcome:

重复前置斜杠与末尾斜杠加入冷热、冻结矩阵，HEAD、404、405 及 Allow 的方法集合一致。

## 实施证据

- [TestRoutingMatrixBeforeAndAfterFreeze](../../../router/router_contract_test.go#L1501)

[批次验证与基准](../review-94305741/README.md)
