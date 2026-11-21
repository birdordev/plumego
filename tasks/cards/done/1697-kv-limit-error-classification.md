# Card 1697 — KV 读取预算超出缺少机器可判结果

Milestone: 稳定模块质量复审
Recipe: specs/change-recipes/fix-bug.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: store
Owned Files: `store/kv/persistence.go`, `store/kv/state_regression_test.go`, `docs/modules/store/README.md`
Depends On: 无

Goal:

KV 读取预算超出缺少机器可判结果。

Scope:

统一构造读取预算错误，先盘点已有 sentinel 及公开影响；需要新增公开分类时独立走 API 审核。

Non-goals:

本卡范围限于所列文件。维持公开签名和现有依赖，兼容契约调整先核对调用边界。

Files:

- `store/kv/persistence.go`
- `store/kv/state_regression_test.go`
- `docs/modules/store/README.md`

## Impact and Ownership

主模块为 `store`。根模块继续使用标准库。拟编辑范围与运行影响见本卡证据及验收。涉及安全与生命周期的变化按公开入口核验。

## 证据

检查号为 14,22,29,41，状态为源码确认。

预读大小及读取后两条边界都返回仅含格式化文案的错误。调用方只能匹配字符串来区分可提高预算的快照和普通 I/O 错误。 [store/kv/persistence.go:73](https://github.com/spcent/plumego/blob/9430574179dc6b793524992833ce27496a5fe548/store/kv/persistence.go#L73)

[复审报告](../../../docs/evidence/stable-quality-review-94305741.md)

## Acceptance Tests

- `store/kv/state_regression_test.go` 中的 `TestSnapshotBudgetErrorDistinguishesReadFailures`
- `store/kv/state_regression_test.go` 中的 `TestOversizedSnapshotStartupReleasesDirectoryLock`

验证两个边界的分类、主 cause 和失败后的目录锁释放。

Tests:

通过所列模块命令运行相邻用例并核对现有约定。

Docs Sync:

复核 `docs/modules/store/README.md`，模块说明改动后核对生成文件差异。

## Validation

```sh
go test -race -timeout 60s ./store/kv
make website-sync
make website-check-generated
```

Done Definition:

- [x] 验证两个边界的分类、主 cause 和失败后的目录锁释放。
- [x] 执行验证命令，记录状态、首次失败和剩余核验范围。

Outcome:

统一预检与读取后超预算的内部错误类型，保持原因链及原可见文案。超预算不改快照且释放目录锁，纠正快照后可重开。经盘点，已有公开错误分别表示坏数据和单值过限，公开分类需单独审核。

## 实施证据

- [TestSnapshotBudgetErrorDistinguishesReadFailures](../../../store/kv/state_regression_test.go#L534)
- [TestOversizedSnapshotStartupReleasesDirectoryLock](../../../store/kv/state_regression_test.go#L583)

[批次验证与基准](../review-94305741/README.md)
