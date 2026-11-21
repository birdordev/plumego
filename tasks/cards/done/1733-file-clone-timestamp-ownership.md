# Card 1733 — 补证 File.Clone 的时间指针和元数据并行归属

Milestone: 稳定模块质量复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: store
Owned Files: `store/file/coverage_test.go`
Depends On: 无

Goal:

补证 File.Clone 的时间指针和元数据并行归属。

Scope:

组合普通时间变化、旧副本与 metadata 变更。

Non-goals:

本卡范围限于所列文件。维持公开签名和现有依赖，兼容契约调整先核对调用边界。

Files:

- `store/file/coverage_test.go`

## Impact and Ownership

主模块为 `store`。根模块继续使用标准库。候审目标是复核可观察契约并记录证据。

## 证据

检查号为 16,34,41，状态为补证。

Clone 同时复制指针型时间和元数据。前两轮重点覆盖容器复杂图。 [store/file/types.go:33](https://github.com/spcent/plumego/blob/9430574179dc6b793524992833ce27496a5fe548/store/file/types.go#L33)

[复审报告](../../../docs/evidence/stable-quality-review-94305741.md)

## Acceptance Tests

- `store/file/coverage_test.go` 中的 `TestFileCloneCopiesMutableFields`

源和两个克隆互不影响，原时间的 nil 约定保留。

Tests:

通过所列模块命令运行相邻用例并核对现有约定。

Docs Sync:

对照原契约核验，若证据要求修订公开行为，先更新实施范围。

## Validation

```sh
go test -race -timeout 60s ./store/file
```

Done Definition:

- [x] 源和两个克隆互不影响，原时间的 nil 约定保留。
- [x] 执行验证命令，记录状态、首次失败和剩余核验范围。

Outcome:

两次克隆的 metadata 与时间指针可分别修改，源与其它克隆不受影响，缺省仍为 nil。

## 实施证据

- [TestFileCloneCopiesMutableFields](../../../store/file/coverage_test.go#L94)

[批次验证与基准](../review-94305741/README.md)
