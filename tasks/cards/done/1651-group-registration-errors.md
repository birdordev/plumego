# Card 1651 — 补证 Group 前缀异常和命名表原子性

Milestone: 稳定模块质量复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: router
Owned Files: `router/reverse_routing_group_test.go`
Depends On: 无

Goal: 补证 Group 前缀异常和命名表原子性。
Scope: 对嵌套前缀校验失败和重复命名单独记录入口行为。
Non-goals: 维持公开签名和依赖；涉及现有兼容契约的调整须先核对调用边界及迁移影响。
Files: `router/reverse_routing_group_test.go`。
Tests: 错误之后未登记额外前缀或名字，已有路由仍可反向生成。
Docs Sync: 对照原契约核验，若证据要求修订公开行为，先更新实施范围。
Done Definition: 完成测试与验收记录，并标明剩余取证范围。
Outcome: 相对组前缀拒绝与重复命名失败分项验证。重复失败后命名表和普通路由表只有原条目。

## 证据

检查号为 15,19,31,37，状态为补证。

Group 前缀的兼容 panic、AddRoute 错误和命名表发布各走独立入口。 [router/registration.go:44](../../../router/registration.go#L44)

[复审报告](../../../docs/evidence/stable-quality-review-09a7f5d3.md)

## 验收与验证

- [x] 错误之后未登记额外前缀或名字，已有路由仍可反向生成。
- [x] 记录验证命令、状态和剩余范围。若使用既有用例满足验收，记录对应证据。

```sh
go test -race -timeout 60s ./router
```

## 实施证据

[TestGroupRejectsRelativePrefix](../../../router/router_contract_test.go#L529) · [TestNamedRouteCollisionAcrossGroupsReturnsError](../../../router/reverse_routing_group_test.go#L135)
