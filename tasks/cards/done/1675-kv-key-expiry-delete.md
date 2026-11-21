# Card 1675 — 明确过期键与 Delete 的存在语义

Milestone: 稳定模块质量复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: store
Owned Files: `store/kv/state_regression_test.go`, `docs/modules/store/README.md`
Depends On: 无

Goal: 明确过期键与 Delete 的存在语义。
Scope: 构造可控过期快照，分别核对公开 Get、Exists 和 Delete。
Non-goals: 维持公开签名和依赖；涉及现有兼容契约的调整须先核对调用边界及迁移影响。
Files: `store/kv/state_regression_test.go`, `docs/modules/store/README.md`。
Tests: 首删、重删、重开数据和返回码均符合记录的契约。
Docs Sync: 对照原契约核验，若证据要求修订公开行为，先更新实施范围。
Done Definition: 完成测试与验收记录，并标明剩余取证范围。
Outcome: 确认已有过期键可删除，重删返回缺失，重开无该键。模块文档记录现有行为。

## 证据

检查号为 6,16,31,41，状态为补证。

Get 对过期键报 ErrKeyExpired，便捷 Delete 查内部 map 后可删除已经失效的键。 [store/kv/kv.go:229](../../../store/kv/kv.go#L229)

[复审报告](../../../docs/evidence/stable-quality-review-09a7f5d3.md)

## 验收与验证

- [x] 首删、重删、重开数据和返回码均符合记录的契约。
- [x] 记录验证命令、状态和剩余范围。若使用既有用例满足验收，记录对应证据。

```sh
go test -race -timeout 60s ./store/kv
```

## 实施证据

[TestDeleteCanRemoveExpiredStoredKey](../../../store/kv/state_regression_test.go#L495)
