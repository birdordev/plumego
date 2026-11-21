# Card 1899 — 量化深路径分派在池边界的分配与生命周期

Milestone: 稳定模块质量复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: router
Owned Files: `router/router_bench_test.go`
Depends On: 无

Goal: 参数和模式在所有测试等价，分配曲线可复验，池未保留超限容量。
Scope: 对相同模式构造 63、64、65 段的普通请求，记录可命中路由、缓存关闭和开启时的 ns/op 与 B/op。
Non-goals: 维持稳定公开签名与标准库依赖，将验证限于本卡范围。
Files: `router/router_bench_test.go`。
Tests: 参数和模式在所有测试等价，分配曲线可复验，池未保留超限容量。
Docs Sync: 记录核验结论；只在现有契约文字需要澄清时另提文档变更。
Done Definition: 完成下列验收，记录验证命令、状态及剩余限制。
Outcome: 记录 63、64、65 段路由在冷分派与缓存命中的分配。复用池容量清理与在途快照测试。

## 证据与影响

第 9 节检查号为 29,34,35,37,48。证据状态为补证。

路径片段超过池上限时丢弃大切片。需要分开观察上限附近的冷分派、缓存命中和在途请求引用。 [router/pool.go:11](../../../router/pool.go#L11)

[复审报告](../../../docs/evidence/stable-quality-review-a8c2f28a.md)

## 验收与验证

- [x] 参数和模式在所有测试等价，分配曲线可复验，池未保留超限容量。
- [x] 记录挂载入口、具体断言和结果。已有测试能覆盖时复用并注明。

```sh
go test -race -timeout 60s ./router
go test ./router -run '^$' -bench '^BenchmarkRoutePathPoolBoundary$' -benchmem -benchtime=100ms -count=3
```

## 实施证据

- [BenchmarkRoutePathPoolBoundary](../../../router/router_bench_test.go#L450)
- [TestRouteStringPoolDiscardsDeepSlicesAndClearsTail](../../../router/router_bench_test.go#L420)
- [TestRouteSnapshotSurvivesCacheEvictionDuringDispatch](../../../router/route_observer_test.go#L37)

[批次验证与基准](../review-a8c2f28a/README.md)
