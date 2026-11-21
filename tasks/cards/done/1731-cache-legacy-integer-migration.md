# Card 1731 — 验证旧整数格式更新后的持久迁移外观

Milestone: 稳定模块质量复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: store
Owned Files: `store/cache/state_regression_test.go`
Depends On: 无

Goal:

验证旧整数格式更新后的持久迁移外观。

Scope:

复用历史 fixture，分别验证正向转换、非整数以及溢出。

Non-goals:

本卡范围限于所列文件。维持公开签名和现有依赖，兼容契约调整先核对调用边界。

Files:

- `store/cache/state_regression_test.go`

## Impact and Ownership

主模块为 `store`。根模块继续使用标准库。候审目标是复核可观察契约并记录证据。

## 证据

检查号为 6,22,31,41，状态为补证。

兼容读取 gob，成功更新走普通字符串写入，错误保留原值。 [store/cache/memory.go:550](https://github.com/spcent/plumego/blob/9430574179dc6b793524992833ce27496a5fe548/store/cache/memory.go#L550)

[复审报告](../../../docs/evidence/stable-quality-review-94305741.md)

## Acceptance Tests

- `store/cache/state_regression_test.go` 中的 `TestLegacyIntegerMigrationPreservesBytesAndTTLOnFailure`

成功的原始字节为规范十进制，失败保留历史值与 TTL。

Tests:

通过所列模块命令运行相邻用例并核对现有约定。

Docs Sync:

对照原契约核验，若证据要求修订公开行为，先更新实施范围。

## Validation

```sh
go test -race -timeout 60s ./store/cache
```

Done Definition:

- [x] 成功的原始字节为规范十进制，失败保留历史值与 TTL。
- [x] 执行验证命令，记录状态、首次失败和剩余核验范围。

Outcome:

旧 gob 整数成功变更为十进制字节，溢出或非整数保留原值和 TTL。

## 实施证据

- [TestLegacyIntegerMigrationPreservesBytesAndTTLOnFailure](../../../store/cache/state_regression_test.go#L329)

[批次验证与基准](../review-94305741/README.md)
