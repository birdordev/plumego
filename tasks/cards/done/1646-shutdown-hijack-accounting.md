# Card 1646 — 验证连接接管和关停计数

Milestone: 稳定模块质量复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: core
Owned Files: `core/lifecycle_test.go`
Depends On: 无

Goal: 验证连接接管和关停计数。
Scope: 用本地 server 桩控制最后一个已接管连接和普通连接。
Non-goals: 维持公开签名和依赖；涉及现有兼容契约的调整须先核对调用边界及迁移影响。
Files: `core/lifecycle_test.go`。
Tests: 确认 Shutdown 只等其所有的连接，接管者负责关闭，计数不为负。
Docs Sync: 对照原契约核验，若证据要求修订公开行为，先更新实施范围。
Done Definition: 完成测试与验收记录，并标明剩余取证范围。
Outcome: 连接追踪在接管时移出连接，仍保留普通连接的 drain。复用现有关停回归核对剩余连接。

## 证据

检查号为 28,29,32,36，状态为补证。

连接被接管后脱离 stdlib 的关停管理，计数器把接管和关闭视为结束。 [core/lifecycle.go:121](../../../core/lifecycle.go#L121)

[复审报告](../../../docs/evidence/stable-quality-review-09a7f5d3.md)

## 验收与验证

- [x] 确认 Shutdown 只等其所有的连接，接管者负责关闭，计数不为负。
- [x] 记录验证命令、状态和剩余范围。若使用既有用例满足验收，记录对应证据。

```sh
go test -race -timeout 60s ./core
```

## 实施证据

[TestConnectionTrackerReleasesHijackedConnections](../../../core/lifecycle_test.go#L1009) · [TestOverlappingShutdownsKeepDrainOwnershipScoped](../../../core/coverage_test.go#L542)
