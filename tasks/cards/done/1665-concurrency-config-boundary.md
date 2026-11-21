# Card 1665 — 补证禁用、中等和最大容量的配置约束

Milestone: 稳定模块质量复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: middleware
Owned Files: `middleware/concurrencylimit/concurrency_limit_test.go`
Depends On: 无

Goal: 补证禁用、中等和最大容量的配置约束。
Scope: 复用整数边界用例，增加禁用与等待深度下限组合。
Non-goals: 维持公开签名和依赖；涉及现有兼容契约的调整须先核对调用边界及迁移影响。
Files: `middleware/concurrencylimit/concurrency_limit_test.go`。
Tests: 下游执行次数以及默认期限、容量返回值不随极值异常偏移。
Docs Sync: 对照原契约核验，若证据要求修订公开行为，先更新实施范围。
Done Definition: 完成测试与验收记录，并标明剩余取证范围。
Outcome: 原有测试覆盖禁用、最大整数容量、负深度和默认队列超时。定向及全量门禁均运行该测试组。

## 证据

检查号为 14,15,29,35，状态为补证。

总容量通过 math.MaxInt 防止溢出，非正并发禁用。 [middleware/concurrencylimit/concurrency_limit.go:55](../../../middleware/concurrencylimit/concurrency_limit.go#L55)

[复审报告](../../../docs/evidence/stable-quality-review-09a7f5d3.md)

## 验收与验证

- [x] 下游执行次数以及默认期限、容量返回值不随极值异常偏移。
- [x] 记录验证命令、状态和剩余范围。若使用既有用例满足验收，记录对应证据。

```sh
go test -race -timeout 60s ./middleware/concurrencylimit
```

## 实施证据

[TestMiddlewareQueueCapacityAtIntegerLimit](../../../middleware/concurrencylimit/concurrency_limit_test.go#L16) · [TestMiddlewareDisabledPassesThrough](../../../middleware/bodylimit/body_limit_test.go#L193) · [TestMiddlewareNegativeQueueDepthTreatedAsZero](../../../middleware/concurrencylimit/concurrency_limit_test.go#L231) · [TestMiddlewareZeroQueueTimeoutUsesDefault](../../../middleware/concurrencylimit/concurrency_limit_test.go#L263)
