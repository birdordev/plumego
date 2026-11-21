# Card 1909 — 核对 App 子组路由选项对同一应用的重入

Milestone: 稳定模块质量复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: core
Owned Files: `core/routing_test.go`
Depends On: 无

Goal: 回调不会卡住，两个命名和路径各由实际成功注册的入口拥有。
Scope: 合成 RouteOption 同步注册另一路由并修改当前元数据，核对重复名称错误后的完整路由表。
Non-goals: 维持稳定公开签名与标准库依赖，将验证限于本卡范围。
Files: `core/routing_test.go`。
Tests: 回调不会卡住，两个命名和路径各由实际成功注册的入口拥有。
Docs Sync: 记录核验结论；只在现有契约文字需要澄清时另提文档变更。
Done Definition: 完成下列验收，记录验证命令、状态及剩余限制。
Outcome: 嵌套组选项能重入注册另一条路由。两个名称与路径只从成功注册进入公开路由表。

## 证据与影响

第 9 节检查号为 19,32,33,36。证据状态为补证。

App 前置状态检查与 router 注册分开。选项内构造另一路由而后回到原嵌套组的可重入性需要证据。 [core/routing.go:154](../../../core/routing.go#L154)

[复审报告](../../../docs/evidence/stable-quality-review-2b94cf86.md)

## 验收与验证

- [x] 回调不会卡住，两个命名和路径各由实际成功注册的入口拥有。
- [x] 记录挂载入口、具体断言和结果。已有测试能覆盖时复用并注明。

```sh
go test -race -timeout 60s ./core
```

## 实施证据

- [TestNestedRouteOptionCanRegisterAnotherAppRoute](../../../core/routing_test.go#L665)

[批次验证与基准](../review-2b94cf86/README.md)
