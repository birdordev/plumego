# Card 1974 — 核对 nil Observer 时不会提前包裹 writer

Milestone: 稳定模块质量复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P3
State: done
Primary Module: middleware/httpmetrics
Owned Files: `middleware/httpmetrics/http_metrics_test.go`
Depends On: 无

Goal: 指标关闭时保持底层能力和返回错误。
Scope: 给公开中间件传 nil，检查 writer 身份与普通 Flush 结果。
Non-goals: 维持稳定公开签名与标准库依赖，将验证限于本卡范围。
Files: `middleware/httpmetrics/http_metrics_test.go`。
Tests: 指标关闭时保持底层能力和返回错误。
Docs Sync: 记录核验结论；只在现有契约文字需要澄清时另提文档变更。
Done Definition: 完成下列验收，记录验证命令、状态及剩余限制。
Outcome: nil Observer 时底层 writer 不换身份，ResponseController.Flush 仍可用。

## 证据与影响

第 9 节检查号为 5,8,29,39，当前证据状态为补证。 [质量规则第 9 节](../../../docs/operations/agent-code-quality-rules.md#L267)

空 Observer 是可选指标装配的入口。现有 TestMiddlewareNilCollectorPassthrough 已覆盖透传，尚需在含 Flush 能力的 writer 上核对包装身份。 [middleware/httpmetrics/http_metrics.go:21](../../../middleware/httpmetrics/http_metrics.go#L21)

[复审报告](../../../docs/evidence/stable-quality-review-999d5e4d.md)

## 验收与验证

- [x] 指标关闭时保持底层能力和返回错误。
- [x] 记录挂载入口、具体断言和结果。已有测试能覆盖时复用并注明。

```sh
go test -race -timeout 60s ./middleware/httpmetrics
```

## 实施证据

- [TestNilObserverPreservesUnderlyingFlush](../../../middleware/httpmetrics/http_metrics_test.go#L164)
