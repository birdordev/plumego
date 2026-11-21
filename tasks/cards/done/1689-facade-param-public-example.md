# Card 1689 — 验证根门面 Param 示例与命名路由的一致性

Milestone: 稳定模块质量复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P3
State: done
Primary Module: .
Owned Files: `plumego_test.go`
Depends On: 无

Goal: 验证根门面 Param 示例与命名路由的一致性。
Scope: 用根门面公开构造和有参数路由测试 Param。
Non-goals: 维持公开签名和依赖；涉及现有兼容契约的调整须先核对调用边界及迁移影响。
Files: `plumego_test.go`。
Tests: 路径参数正确，缺失参数返回空值；根包批准的导出集合不变。
Docs Sync: 对照原契约核验，若证据要求修订公开行为，先更新实施范围。
Done Definition: 完成测试与验收记录，并标明剩余取证范围。
Outcome: 从根门面构造应用并通过 handler 验证 Param 命中和缺失。

## 证据

检查号为 3,6,19,37，状态为补证。

根包已经回归构造、地址和 ServeHTTP。门面只委托 router.Param，示例需要联合匹配与缺失值的证据。 [plumego.go:73](../../../plumego.go#L73)

[复审报告](../../../docs/evidence/stable-quality-review-09a7f5d3.md)

## 验收与验证

- [x] 路径参数正确，缺失参数返回空值；根包批准的导出集合不变。
- [x] 记录验证命令、状态和剩余范围。若使用既有用例满足验收，记录对应证据。

```sh
go test -race -timeout 60s .
```

## 实施证据

[TestParamFromRootFacade](../../../plumego_test.go#L158)
