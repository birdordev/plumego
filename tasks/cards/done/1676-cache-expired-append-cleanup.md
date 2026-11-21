# Card 1676 — 核对清除过期项后写入拒绝的可观察状态

Milestone: 稳定模块质量复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: store
Owned Files: `store/cache/state_regression_test.go`
Depends On: 无

Goal: 核对清除过期项后写入拒绝的可观察状态。
Scope: 使用合成时钟和小配额观察写拒绝、过期读和统计。
Non-goals: 维持公开签名和依赖；涉及现有兼容契约的调整须先核对调用边界及迁移影响。
Files: `store/cache/state_regression_test.go`。
Tests: 过期项的惰性清理与失败写的边界明确，重试可成功。
Docs Sync: 对照原契约核验，若证据要求修订公开行为，先更新实施范围。
Done Definition: 完成测试与验收记录，并标明剩余取证范围。
Outcome: 用合成时钟复现过期项清理与容量失败组合。未存入失败值、清理已完成，合法重试成功。

## 证据

检查号为 28,30,31,35,41，状态为补证。

上轮验证未过期值容量失败不变。本轮关注已过期项被移除后，新写入因预算失败。 [store/cache/memory.go:404](../../../store/cache/memory.go#L404)

[复审报告](../../../docs/evidence/stable-quality-review-09a7f5d3.md)

## 验收与验证

- [x] 过期项的惰性清理与失败写的边界明确，重试可成功。
- [x] 记录验证命令、状态和剩余范围。若使用既有用例满足验收，记录对应证据。

```sh
go test -race -timeout 60s ./store/cache
```

## 实施证据

[TestExpiredAppendFailureDropsExpiredItemAndAllowsRetry](../../../store/cache/state_regression_test.go#L236)
