# Card 1680 — 核对不可序列化详情对探测输出的影响

Milestone: 稳定模块质量复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: x/observability
Owned Files: `x/observability/ops/healthhttp/helpers.go`, `x/observability/ops/healthhttp/handlers_test.go`, `docs/modules/x/observability/ops/README.md`
Depends On: 无

Goal: 核对不可序列化详情对探测输出的影响。
Scope: 由健康模型审查牵头，仅核对宿主的安全回退响应及状态。
Non-goals: 维持公开签名和依赖；涉及现有兼容契约的调整须先核对调用边界及迁移影响。
Files: `x/observability/ops/healthhttp/helpers.go`, `x/observability/ops/healthhttp/handlers_test.go`, `docs/modules/x/observability/ops/README.md`。
Tests: 一项不支持的占位详情不会模糊 readiness 的状态及终态。
Docs Sync: 对照原契约核验，若证据要求修订公开行为，先更新实施范围。
Done Definition: 完成测试与验收记录，并标明剩余取证范围。
Outcome: 健康宿主在提交前编码。失败返回固定错误文本，已有 unhealthy 仍返回 503，readiness 继续从不含详情的模型生成。

## 证据

检查号为 21,23,34,42,46，状态为补证。

稳定模型允许任意 Details 类型，调用方负责 JSON。对模型零值已有覆盖，详情编码失败还需在宿主入口取证。 [health/core.go:31](../../../health/core.go#L31)

[复审报告](../../../docs/evidence/stable-quality-review-09a7f5d3.md)

## 验收与验证

- [x] 一项不支持的占位详情不会模糊 readiness 的状态及终态。
- [x] 记录验证命令、状态和剩余范围。若使用既有用例满足验收，记录对应证据。

```sh
go test -race -timeout 60s ./x/observability/ops/healthhttp
```

## 实施证据

[TestHealthEncodingFailureKeepsUnhealthyStatusAndSafeBody](../../../x/observability/ops/healthhttp/handlers_test.go#L485)
