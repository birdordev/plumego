# Card 1647 — 验证构造失败与路由选项快照交错

Milestone: 稳定模块质量复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: core
Owned Files: `core/routing_test.go`
Depends On: 无

Goal: 验证构造失败与路由选项快照交错。
Scope: 阻塞选项并让首次中间件组装返回 nil，然后释放选项。
Non-goals: 维持公开签名和依赖；涉及现有兼容契约的调整须先核对调用边界及迁移影响。
Files: `core/routing_test.go`。
Tests: 注册错误可见、重试组装后无半注册路由、命名路由和 Routes 一致。
Docs Sync: 对照原契约核验，若证据要求修订公开行为，先更新实施范围。
Done Definition: 完成测试与验收记录，并标明剩余取证范围。
Outcome: 通过通道确保首次失败组装冻结后，阻塞的路由选项无法发布路由或命名项。

## 证据

检查号为 19,28,31,36,47，状态为补证。

选项回调在路由锁外，准备冻结可发生在选项执行和发布之间。 [core/routing.go:31](../../../core/routing.go#L31)

[复审报告](../../../docs/evidence/stable-quality-review-09a7f5d3.md)

## 验收与验证

- [x] 注册错误可见、重试组装后无半注册路由、命名路由和 Routes 一致。
- [x] 记录验证命令、状态和剩余范围。若使用既有用例满足验收，记录对应证据。

```sh
go test -race -timeout 60s ./core
```

## 实施证据

[TestRouteOptionCannotPublishAfterFailedBuildFreezesRouter](../../../core/routing_test.go#L583)
