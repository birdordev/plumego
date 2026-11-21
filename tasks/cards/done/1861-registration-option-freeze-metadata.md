# Card 1861 — 核对 RouteOption 执行期间 Freeze 的发表边界

Milestone: 稳定模块质量复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: router
Owned Files: `router/router_contract_test.go`
Depends On: 无

Goal: 迟到的路由没有部分发布，原路由和 Allow 全程可用。
Scope: 让可控选项暂停，Freeze 后释放，读取 Routes、NamedRoutes，并请求新旧路径。
Non-goals: 维持稳定公开签名与标准库依赖，将验证限于本卡范围。
Files: `router/router_contract_test.go`。
Tests: 迟到的路由没有部分发布，原路由和 Allow 全程可用。
Docs Sync: 记录核验结论；只在现有契约文字需要澄清时另提文档变更。
Done Definition: 完成下列验收，记录验证命令、状态及剩余限制。
Outcome: 冻结发生在路由选项回调期间时，迟到的注册不会影响路径、名称或原路由的 Allow。

## 证据与影响

第 9 节检查号为 19,28,31,33,37。证据状态为补证。

路由选项锁外执行。冻结可在该回调完成前发布，需在同一用例核对命名表、树及 405。 [router/registration.go:92](../../../router/registration.go#L92)

[复审报告](../../../docs/evidence/stable-quality-review-a8c2f28a.md)

## 验收与验证

- [x] 迟到的路由没有部分发布，原路由和 Allow 全程可用。
- [x] 记录挂载入口、具体断言和结果。已有测试能覆盖时复用并注明。

```sh
go test -race -timeout 60s ./router
```

## 实施证据

- [TestRouteOptionOverlappingFreezeDoesNotPublishMetadata](../../../router/router_contract_test.go#L1598)

[批次验证与基准](../review-a8c2f28a/README.md)
