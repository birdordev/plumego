# Card 1973 — 核对 nil tracer 时请求和 writer 原样交付

Milestone: 稳定模块质量复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P3
State: done
Primary Module: middleware/tracing
Owned Files: `middleware/tracing/tracing_test.go`
Depends On: 无

Goal: 普通响应可写，context 及 writer 原样传递。
Scope: 安装 nil tracer 与观察型 handler，捕获请求和 writer 身份。
Non-goals: 维持稳定公开签名与标准库依赖，将验证限于本卡范围。
Files: `middleware/tracing/tracing_test.go`。
Tests: 普通响应可写，context 及 writer 原样传递。
Docs Sync: 记录核验结论；只在现有契约文字需要澄清时另提文档变更。
Done Definition: 完成下列验收，记录验证命令、状态及剩余限制。
Outcome: nil tracer 时请求、Context 和 writer 原样传递。

## 证据与影响

第 9 节检查号为 5,8,25,39，当前证据状态为补证。 [质量规则第 9 节](../../../docs/operations/agent-code-quality-rules.md#L267)

Tracing 显式支持不装配 tracer。现有 TestMiddlewareNilTracerPassthrough 检查调用和状态，尚未断言请求 context 与 writer 身份。 [middleware/tracing/tracing.go:37](../../../middleware/tracing/tracing.go#L37)

[复审报告](../../../docs/evidence/stable-quality-review-999d5e4d.md)

## 验收与验证

- [x] 普通响应可写，context 及 writer 原样传递。
- [x] 记录挂载入口、具体断言和结果。已有测试能覆盖时复用并注明。

```sh
go test -race -timeout 60s ./middleware/tracing
```

## 实施证据

- [TestNilTracerKeepsWriterAndRequestIdentity](../../../middleware/tracing/tracing_test.go#L194)
