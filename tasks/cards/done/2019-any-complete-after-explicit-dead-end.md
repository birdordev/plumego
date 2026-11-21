# Card 2019 — 显式方法分支落空后回到完整 Any

Milestone: 稳定模块第 9 节复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: router
Owned Files: `router/router_contract_test.go`
Depends On: 无

Goal: 显式方法分支落空后回到完整 Any
Scope: 注册显式长路由与 Any 短路由，让显式分支仅部分匹配。
Non-goals: 范围限于本卡涉及的契约，保持公开签名和依赖。
Files: `router/router_contract_test.go`。
Tests: 冷热缓存均运行 Any 完整匹配，HEAD 和其他不重叠路径的状态保持一致。
Docs Sync: 核对新增验收与已有模块说明一致，记录证据和限制。
Done Definition: 完成以下验收，记录命令、结果与剩余限制。
Outcome: 完整 Any 在显式方法死端时获选，冷热结果与 HEAD、404、405 和 Allow 的预期一致。

## 证据与影响

证据状态为补证。第 9 节检查号为 19,37,45,46。 [质量规则第 9 节](../../../docs/operations/agent-code-quality-rules.md#L267)

ServeHTTP 在方法树、Any 与 405 回退之间选择，需要同组同层的死端组合证据。 [router/dispatch.go:178](../../../router/dispatch.go#L178)

[复审报告](../../../docs/evidence/stable-quality-review-c3a77410.md)

## 验收与验证

- [x] 冷热缓存均运行 Any 完整匹配，HEAD 和其他不重叠路径的状态保持一致。
- [x] 已有测试覆盖相同触发和断言时可直接复用，记录具体用例。

```sh
go test -race -timeout 60s ./router
```

## 实施证据

- [TestCompleteAnyRouteWinsWhenExplicitMethodDeadEnds](../../../router/router_contract_test.go#L1761)
