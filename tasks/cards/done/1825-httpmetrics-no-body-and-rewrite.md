# Card 1825 — 核对无正文状态与实际录入的 HTTP 指标

Milestone: 稳定模块质量复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: middleware
Owned Files: `middleware/httpmetrics/http_metrics_test.go`
Depends On: 无

Goal: 每个请求一次记录，状态、原计数和路由一致。
Scope: 单链路固定三个响应状态和已匹配模式，观察一个收集器。
Non-goals: 维持稳定公开签名与标准库依赖，将验证限于本卡范围。
Files: `middleware/httpmetrics/http_metrics_test.go`。
Tests: 每个请求一次记录，状态、原计数和路由一致。
Docs Sync: 复核本卡涉及的已公开约定。
Done Definition: 完成下列验收，记录验证命令、状态及剩余限制。
Outcome: 无正文响应使用匹配路由模板且只观察一次。

## 证据与影响

第 9 节检查号为 24,27,39,44。证据状态为补证。

中间件统计基于 recorder 与 route。需要覆盖 204、304 与 HEAD 的无正文，避免以请求的原标识重复计数。 [middleware/httpmetrics/http_metrics.go:21](../../../middleware/httpmetrics/http_metrics.go#L21)

[复审报告](../../../docs/evidence/stable-quality-review-a074eadb.md)

## 验收与验证

- [x] 每个请求一次记录，状态、原计数和路由一致。
- [x] 记录运行结果。若已有用例覆盖目标，记录用例及相关断言。

```sh
go test -race -timeout 60s ./middleware/httpmetrics
```

## 实施证据

- [TestObservesBodylessRequestsOnceWithMatchedRoute](../../../middleware/httpmetrics/http_metrics_test.go#L136)

[批次验证与基准](../review-a074eadb/README.md)
