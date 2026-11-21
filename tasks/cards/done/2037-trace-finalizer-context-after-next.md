# Card 2037 — Tracing 结束时观察到派生上下文

Milestone: 稳定模块第 9 节复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P3
State: done
Primary Module: middleware/tracing
Owned Files: `middleware/tracing/tracing_regression_test.go`
Depends On: 无

Goal: Tracing 结束时观察到派生上下文
Scope: 用计数 tracer 标记派生值，在 next 验证并记录结束顺序。
Non-goals: 范围限于本卡涉及的契约，保持公开签名和依赖。
Files: `middleware/tracing/tracing_regression_test.go`。
Tests: 启动、处理、收尾恰一次，派生上下文的父取消仍贯通。
Docs Sync: 核对新增验收与已有模块说明一致，记录证据和限制。
Done Definition: 完成以下验收，记录命令、结果与剩余限制。
Outcome: 派生上下文保留父取消与期限。进入下游时还未结束 span，返回后恰好结束一次。

## 证据与影响

证据状态为补证。第 9 节检查号为 20,26,27,39。 [质量规则第 9 节](../../../docs/operations/agent-code-quality-rules.md#L267)

Tracing 从 tracer 接手派生上下文，结束回调应围绕同一条请求执行。 [middleware/tracing/tracing.go:37](../../../middleware/tracing/tracing.go#L37)

[复审报告](../../../docs/evidence/stable-quality-review-c3a77410.md)

## 验收与验证

- [x] 启动、处理、收尾恰一次，派生上下文的父取消仍贯通。
- [x] 已有测试覆盖相同触发和断言时可直接复用，记录具体用例。

```sh
go test -race -timeout 60s ./middleware/tracing
```

## 实施证据

- [TestTracingPreservesParentDeadlineOnCanceledRequest](../../../middleware/tracing/tracing_regression_test.go#L115)
