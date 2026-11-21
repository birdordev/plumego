# Card 1808 — 核对旧 RouteGroup 的 handler 错误与冻结顺序

Milestone: 稳定模块质量复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: core
Owned Files: `core/routing_test.go`
Depends On: 无

Goal: 冻结后无发布，原有路由仍分派。
Scope: 同一 group 冻结前后传入空 handler 及有效路由，核对错误分类。
Non-goals: 维持稳定公开签名与标准库依赖，将验证限于本卡范围。
Files: `core/routing_test.go`。
Tests: 冻结后无发布，原有路由仍分派。
Docs Sync: 复核本卡涉及的已公开约定。
Done Definition: 完成下列验收，记录验证命令、状态及剩余限制。
Outcome: 冻结后持有的旧 RouteGroup 仍先检查冻结状态，包括空 handler。

## 证据与影响

第 9 节检查号为 19,22,28,36。证据状态为补证。

App 和 Group 有对应注册边界，保留的 group 句柄跨冻结需要与根操作对照。 [core/routing.go:154](../../../core/routing.go#L154)

[复审报告](../../../docs/evidence/stable-quality-review-a074eadb.md)

## 验收与验证

- [x] 冻结后无发布，原有路由仍分派。
- [x] 记录运行结果。若已有用例覆盖目标，记录用例及相关断言。

```sh
go test -race -timeout 60s ./core
```

## 实施证据

- [TestOldRouteGroupChecksFreezeBeforeNilHandler](../../../core/routing_test.go#L640)

[批次验证与基准](../review-a074eadb/README.md)
