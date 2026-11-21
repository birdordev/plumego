# Card 1678 — 验证 Clear 与已取得值副本的隔离

Milestone: 稳定模块质量复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: store
Owned Files: `store/cache/state_regression_test.go`
Depends On: 无

Goal: 验证 Clear 与已取得值副本的隔离。
Scope: 屏障控制复制完成和 Clear，同时获取统计。
Non-goals: 维持公开签名和依赖；涉及现有兼容契约的调整须先核对调用边界及迁移影响。
Files: `store/cache/state_regression_test.go`。
Tests: 副本保留独立，Clear 完成后新读返回缺失，并准确重建统计。
Docs Sync: 对照原契约核验，若证据要求修订公开行为，先更新实施范围。
Done Definition: 完成测试与验收记录，并标明剩余取证范围。
Outcome: 受控并发 Clear 不改变已交付读取副本。清空后统计和新读一致。

## 证据

检查号为 28,32,34,41，状态为补证。

Clear 清空内部数据，Get 已复制给调用者的值仍可能被其它 goroutine 使用。 [store/cache/memory.go:308](../../../store/cache/memory.go#L308)

[复审报告](../../../docs/evidence/stable-quality-review-09a7f5d3.md)

## 验收与验证

- [x] 副本保留独立，Clear 完成后新读返回缺失，并准确重建统计。
- [x] 记录验证命令、状态和剩余范围。若使用既有用例满足验收，记录对应证据。

```sh
go test -race -timeout 60s ./store/cache
```

## 实施证据

[TestClearDoesNotInvalidateReaderCopy](../../../store/cache/state_regression_test.go#L262)
