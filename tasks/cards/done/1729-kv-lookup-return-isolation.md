# Card 1729 — 补证历史快照加载后 Get 副本生命周期

Milestone: 稳定模块质量复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: store
Owned Files: `store/kv/kv_test.go`
Depends On: 无

Goal:

补证历史快照加载后 Get 副本生命周期。

Scope:

读出旧 fixture 后写入不同值，修改两个时间点的返回数据。

Non-goals:

本卡范围限于所列文件。维持公开签名和现有依赖，兼容契约调整先核对调用边界。

Files:

- `store/kv/kv_test.go`

## Impact and Ownership

主模块为 `store`。根模块继续使用标准库。候审目标是复核可观察契约并记录证据。

## 证据

检查号为 29,32,34,41，状态为补证。

加载使用解码后的数据，Get 始终复制。需结合重开、覆盖及旧副本修改。 [store/kv/kv.go:200](https://github.com/spcent/plumego/blob/9430574179dc6b793524992833ce27496a5fe548/store/kv/kv.go#L200)

[复审报告](../../../docs/evidence/stable-quality-review-94305741.md)

## Acceptance Tests

- `store/kv/kv_test.go` 中的 `TestHistoricalSnapshotGetDetachesAcrossOverwriteAndReopen`

重开内容与最后写一致，旧值或副本修改不污染持久状态。

Tests:

通过所列模块命令运行相邻用例并核对现有约定。

Docs Sync:

对照原契约核验，若证据要求修订公开行为，先更新实施范围。

## Validation

```sh
go test -race -timeout 60s ./store/kv
```

Done Definition:

- [x] 重开内容与最后写一致，旧值或副本修改不污染持久状态。
- [x] 执行验证命令，记录状态、首次失败和剩余核验范围。

Outcome:

历史快照首次加载及覆盖后返回独立字节副本，修改两个副本不影响最后写入的重开值。

## 实施证据

- [TestHistoricalSnapshotGetDetachesAcrossOverwriteAndReopen](../../../store/kv/kv_test.go#L1896)

[批次验证与基准](../review-94305741/README.md)
