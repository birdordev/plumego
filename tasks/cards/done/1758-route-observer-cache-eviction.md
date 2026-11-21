# Card 1758 — 核对缓存逐出与 in-flight 路由上下文

Milestone: 稳定模块质量复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: router
Owned Files: `router/route_observer_test.go`
Depends On: 无

Goal: 缓存冷热与逐出前后观察一致，状态没有提前归还。
Scope: 阻塞第一请求，触发容量逐出后释放，断言两个 route 名和参数。
Non-goals: 维持稳定公开签名与标准库依赖，将验证限于本卡范围。
Files: `router/route_observer_test.go`。
Tests: 缓存冷热与逐出前后观察一致，状态没有提前归还。
Docs Sync: 复核本卡涉及的已公开约定。
Done Definition: 完成下列验收，记录验证命令、状态及剩余限制。
Outcome: 单槽缓存被新请求逐出时，在途 handler 和路由观测继续持有各自参数及路由名。

## 证据与影响

第 9 节检查号为 29,32,34,37。证据状态为补证。

缓存持有安装后的 RouteState，逐出不应使在途观察回调看到别的路由数据。 [router/dispatch.go:223](../../../router/dispatch.go#L223)

[复审报告](../../../docs/evidence/stable-quality-review-d224acbc.md)

## 验收与验证

- [x] 缓存冷热与逐出前后观察一致，状态没有提前归还。
- [x] 记录运行结果。若已有用例覆盖目标，记录用例及相关断言。

```sh
go test -race -timeout 60s ./router
```

## 实施证据

- [TestRouteSnapshotSurvivesCacheEvictionDuringDispatch](../../../router/route_observer_test.go#L37)

[批次验证与基准](../review-d224acbc/README.md)
