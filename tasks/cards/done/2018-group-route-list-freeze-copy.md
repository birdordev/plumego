# Card 2018 — 冻结前后 RouteInfo 快照隔离

Milestone: 稳定模块第 9 节复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P3
State: done
Primary Module: core
Owned Files: `core/routing_test.go`
Depends On: 无

Goal: 冻结前后 RouteInfo 快照隔离
Scope: 对已分组路由改写读取快照的条目及标签，跨 Prepare 再读。
Non-goals: 范围限于本卡涉及的契约，保持公开签名和依赖。
Files: `core/routing_test.go`。
Tests: 原方法、绝对 pattern、名称及标签保持原值。
Docs Sync: 核对新增验收与已有模块说明一致，记录证据和限制。
Done Definition: 完成以下验收，记录命令、结果与剩余限制。
Outcome: 跨 Prepare 修改旧快照无影响，分组完整路径、方法、名字与标签保持原值。

## 证据与影响

证据状态为补证。第 9 节检查号为 19,34,36。 [质量规则第 9 节](../../../docs/operations/agent-code-quality-rules.md#L267)

App.Routes 直接暴露路由快照 API。需要从应用入口检验分组绝对路径和 Tags 的独立所有权。 [core/app.go:70](../../../core/app.go#L70)

[复审报告](../../../docs/evidence/stable-quality-review-c3a77410.md)

## 验收与验证

- [x] 原方法、绝对 pattern、名称及标签保持原值。
- [x] 已有测试覆盖相同触发和断言时可直接复用，记录具体用例。

```sh
go test -race -timeout 60s ./core
```

## 实施证据

- [TestGroupRouteSnapshotsRemainIndependentAcrossPrepare](../../../core/routing_test.go#L718)
