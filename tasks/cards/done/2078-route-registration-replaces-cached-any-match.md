# Card 2078 — 新增显式方法使已缓存 Any 匹配失效

Milestone: 稳定模块第 9 节复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: router
Owned Files: `router/router_contract_test.go`
Depends On: 无

Goal: 新增显式方法使已缓存 Any 匹配失效
Scope: 重复 GET 形成 Any 热路径，再附命名元数据注册显式 GET。在另一条只有 POST 的普通路径重复请求后新增 PUT。
Non-goals: 范围限于本卡涉及的契约，保持公开签名和依赖。
Files: `router/router_contract_test.go`。
Tests: GET 立即到达新处理器并带新名字，另一条路径的 405 Allow 包含 PUT 且稳定。
Docs Sync: 核对新增验收与已有模块说明一致，记录证据和限制。
Done Definition: 完成以下验收，记录命令、结果与剩余限制。
Outcome: 先运行 Any 热缓存，再注册显式 GET，下一次请求看到新方法与名字。独立方法列表的 Allow 同步刷新。

## 证据与影响

证据状态为补证。第 9 节检查号为 19,31,32,37,47。 [质量规则第 9 节](../../../docs/operations/agent-code-quality-rules.md#L267)

成功的 Any 匹配会缓存，404 和 405 通过各自分支计算。新增显式方法后应同时刷新已有命中和相关方法表。 [router/registration.go:195](../../../router/registration.go#L195)

[复审报告](../../../docs/evidence/stable-quality-review-2f962d21.md)

## 验收与验证

- [x] GET 立即到达新处理器并带新名字，另一条路径的 405 Allow 包含 PUT 且稳定。
- [x] 已有测试覆盖相同触发和断言时可直接复用，记录具体用例。

```sh
go test -race -timeout 60s ./router
```

## 实施证据

- [TestRegistrationReplacesHotAnyAndRefreshesAllow](../../../router/router_contract_test.go#L1798)
