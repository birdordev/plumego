# Card 1915 — 核对未初始化和空组的命名路由快照

Milestone: 稳定模块质量复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P3
State: done
Primary Module: router
Owned Files: `router/router_contract_test.go`
Depends On: 无

Goal: 空 map 可修改、共享 Router 保持全量视图、参数副本独立。
Scope: 修改一次 NamedRoutes 返回 map 和内部 ParamPos，再用 URL 与另一份快照校验。
Non-goals: 维持稳定公开签名与标准库依赖，将验证限于本卡范围。
Files: `router/router_contract_test.go`。
Tests: 空 map 可修改、共享 Router 保持全量视图、参数副本独立。
Docs Sync: 记录核验结论；只在现有契约文字需要澄清时另提文档变更。
Done Definition: 完成下列验收，记录验证命令、状态及剩余限制。
Outcome: 零 Router 返回可修改空表，子组可查看命名路由。修改返回 ParamPos 和 map 不影响原路由和 URL。

## 证据与影响

第 9 节检查号为 6,16,34,37。证据状态为补证。

空 Router 与组共享状态时的命名表快照来自同一 API。需要对照零值、根组和拥有命名路由的子组。 [router/metadata.go:152](../../../router/metadata.go#L152)

[复审报告](../../../docs/evidence/stable-quality-review-2b94cf86.md)

## 验收与验证

- [x] 空 map 可修改、共享 Router 保持全量视图、参数副本独立。
- [x] 记录挂载入口、具体断言和结果。已有测试能覆盖时复用并注明。

```sh
go test -race -timeout 60s ./router
```

## 实施证据

- [TestNamedRouteSnapshotsStayOwnedAcrossZeroAndGroups](../../../router/router_contract_test.go#L1668)

[批次验证与基准](../review-2b94cf86/README.md)
