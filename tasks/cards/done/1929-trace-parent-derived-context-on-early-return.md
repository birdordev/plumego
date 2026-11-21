# Card 1929 — 核对合规 tracer 保持父取消及 trace 元数据

Milestone: 稳定模块质量复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: middleware
Owned Files: `middleware/tracing/tracing_regression_test.go`
Depends On: 无

Goal: 取消可观察，相关元数据和已有 deadline 保留，span 只收尾一次。
Scope: 通过公开 Middleware 注入合规派生 tracer，记录下游 Context 的取消原因、已继承字段与 span 终态。
Non-goals: 维持稳定公开签名与标准库依赖，将验证限于本卡范围。
Files: `middleware/tracing/tracing_regression_test.go`。
Tests: 取消可观察，相关元数据和已有 deadline 保留，span 只收尾一次。
Docs Sync: 记录核验结论；只在现有契约文字需要澄清时另提文档变更。
Done Definition: 完成下列验收，记录验证命令、状态及剩余限制。
Outcome: 合规 tracer 派生上下文继续保留父截止时间、取消、请求 ID 与子值。下游调用、span 收尾各一次。

## 证据与影响

第 9 节检查号为 26,27,33,39。证据状态为补证。

Tracer 返回派生 Context，BeginTrace 在缺少完整 ID 时可用 span 回填。需联测先取消与继承短截止。 [middleware/internal/telemetry/helpers.go:89](../../../middleware/internal/telemetry/helpers.go#L89)

[复审报告](../../../docs/evidence/stable-quality-review-2b94cf86.md)

## 验收与验证

- [x] 取消可观察，相关元数据和已有 deadline 保留，span 只收尾一次。
- [x] 记录挂载入口、具体断言和结果。已有测试能覆盖时复用并注明。

```sh
go test -race -timeout 60s ./middleware/tracing
```

## 实施证据

- [TestTracingPreservesParentDeadlineOnCanceledRequest](../../../middleware/tracing/tracing_regression_test.go#L115)

[批次验证与基准](../review-2b94cf86/README.md)
