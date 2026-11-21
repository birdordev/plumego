# Card 1771 — 核对 tracer 派生 Context 的 deadline 传递

Milestone: 稳定模块质量复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: middleware
Owned Files: `middleware/internal/telemetry/helpers_test.go`
Depends On: 无

Goal: 下游取消跟随约定父请求，已有效 span 收尾一次。
Scope: 用普通可控 tracer 记录原 Context，移植合法 trace 并分别取消。
Non-goals: 维持稳定公开签名与标准库依赖，将验证限于本卡范围。
Files: `middleware/internal/telemetry/helpers_test.go`。
Tests: 下游取消跟随约定父请求，已有效 span 收尾一次。
Docs Sync: 复核本卡涉及的已公开约定。
Done Definition: 完成下列验收，记录验证命令、状态及剩余限制。
Outcome: starter 从入站上下文派生的 deadline 可见，父取消传播，追踪 span 只收尾一次。

## 证据与影响

第 9 节检查号为 26,27,33,39。证据状态为补证。

starter 返回的 Context 可能携带独立 deadline；需明确请求原取消与替代上下文的约定。 [middleware/internal/telemetry/helpers.go:70](../../../middleware/internal/telemetry/helpers.go#L70)

[复审报告](../../../docs/evidence/stable-quality-review-d224acbc.md)

## 验收与验证

- [x] 下游取消跟随约定父请求，已有效 span 收尾一次。
- [x] 记录运行结果。若已有用例覆盖目标，记录用例及相关断言。

```sh
go test -race -timeout 60s ./middleware/internal/telemetry
```

## 实施证据

- [TestBeginTracePreservesDerivedDeadlineAndParentCancellation](../../../middleware/internal/telemetry/helpers_test.go#L268)

[批次验证与基准](../review-d224acbc/README.md)
