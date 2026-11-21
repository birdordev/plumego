# Card 1649 — 验证 drain 间隔零值与负值含义

Milestone: 稳定模块质量复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P3
State: done
Primary Module: core
Owned Files: `core/lifecycle_test.go`
Depends On: 无

Goal: 验证 drain 间隔零值与负值含义。
Scope: 复用构造测试，断言三个场景的生效间隔。
Non-goals: 维持公开签名和依赖；涉及现有兼容契约的调整须先核对调用边界及迁移影响。
Files: `core/lifecycle_test.go`。
Tests: 不会影响 HTTP 的闲置与头部期限。
Docs Sync: 对照原契约核验，若证据要求修订公开行为，先更新实施范围。
Done Definition: 完成测试与验收记录，并标明剩余取证范围。
Outcome: 锁定负值、零值与正值的 drain 间隔约定，独立于 HTTP 空闲和读头超时。

## 证据

检查号为 6,14,16,36，状态为补证。

默认配置与连接追踪构造两层定义了 drain 间隔，需固定零与负的默认策略，正值应原样使用。 [core/lifecycle.go:111](../../../core/lifecycle.go#L111)

[复审报告](../../../docs/evidence/stable-quality-review-09a7f5d3.md)

## 验收与验证

- [x] 不会影响 HTTP 的闲置与头部期限。
- [x] 记录验证命令、状态和剩余范围。若使用既有用例满足验收，记录对应证据。

```sh
go test -race -timeout 60s ./core
```

## 实施证据

[TestConnectionTrackerDrainIntervalDefaults](../../../core/lifecycle_test.go#L1028) · [TestDefaultConfigAllFields](../../../core/config_test.go#L8)
