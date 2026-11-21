# Card 1681 — 补证健康检查取消时的宿主缓存粒度

Milestone: 稳定模块质量复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: x/observability
Owned Files: `x/observability/ops/healthhttp/component_regression_test.go`
Depends On: 无

Goal: 补证健康检查取消时的宿主缓存粒度。
Scope: 观察一次被取消的检查和后来成功检查，验证每个阶段的缓存通过正常 API 可见。
Non-goals: 维持公开签名和依赖；涉及现有兼容契约的调整须先核对调用边界及迁移影响。
Files: `x/observability/ops/healthhttp/component_regression_test.go`。
Tests: 取消分类可观察，新成功快照滞后边界清晰。
Docs Sync: 对照原契约核验，若证据要求修订公开行为，先更新实施范围。
Done Definition: 完成测试与验收记录，并标明剩余取证范围。
Outcome: 取消的单项探测更新为 unhealthy 和零尝试次数，后来成功恢复健康缓存，响应状态分别为 503/200。

## 证据

检查号为 26,32,42,46，状态为补证。

模型接口显式接受 Context，宿主负责何时更新组件状态和聚合结果。 [health/core.go:24](../../../health/core.go#L24)

[复审报告](../../../docs/evidence/stable-quality-review-09a7f5d3.md)

## 验收与验证

- [x] 取消分类可观察，新成功快照滞后边界清晰。
- [x] 记录验证命令、状态和剩余范围。若使用既有用例满足验收，记录对应证据。

```sh
go test -race -timeout 60s ./x/observability/ops/healthhttp
```

## 实施证据

[TestComponentHealthCanceledProbeFailsClosed](../../../x/observability/ops/healthhttp/component_regression_test.go#L93)
