# Card 1728 — 确认读过期键后内存与持久目录不变

Milestone: 稳定模块质量复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: store
Owned Files: `store/kv/state_regression_test.go`
Depends On: 无

Goal:

确认读过期键后内存与持久目录不变。

Scope:

在可控过期内存和已写状态上读取并对照磁盘哈希。

Non-goals:

本卡范围限于所列文件。维持公开签名和现有依赖，兼容契约调整先核对调用边界。

Files:

- `store/kv/state_regression_test.go`

## Impact and Ownership

主模块为 `store`。根模块继续使用标准库。候审目标是复核可观察契约并记录证据。

## 证据

检查号为 28,31,34,41，状态为补证。

已有删除过期键与重开测试。Get 只增 miss 并保持快照，这与 Exists、Keys 的自动过滤不同。 [store/kv/kv.go:196](https://github.com/spcent/plumego/blob/9430574179dc6b793524992833ce27496a5fe548/store/kv/kv.go#L196)

[复审报告](../../../docs/evidence/stable-quality-review-94305741.md)

## Acceptance Tests

- `store/kv/state_regression_test.go` 中的 `TestExpiredEntryReadsPreserveStoredSnapshot`

纯读不改磁盘，诊断、计数与返回值有清晰分工。

Tests:

通过所列模块命令运行相邻用例并核对现有约定。

Docs Sync:

对照原契约核验，若证据要求修订公开行为，先更新实施范围。

## Validation

```sh
go test -race -timeout 60s ./store/kv
```

Done Definition:

- [x] 纯读不改磁盘，诊断、计数与返回值有清晰分工。
- [x] 执行验证命令，记录状态、首次失败和剩余核验范围。

Outcome:

合成时钟推进后，Get、Exists、Keys 均对外报告过期结果，已保存的内存记录与磁盘字节保留。

## 实施证据

- [TestExpiredEntryReadsPreserveStoredSnapshot](../../../store/kv/state_regression_test.go#L554)

[批次验证与基准](../review-94305741/README.md)
