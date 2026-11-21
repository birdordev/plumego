# Card 2036 — 指标观察底层短写实际字节

Milestone: 稳定模块第 9 节复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: middleware/httpmetrics
Owned Files: `middleware/httpmetrics/capture_regression_test.go`
Depends On: 无

Goal: 指标观察底层短写实际字节
Scope: 注入可区分已写字节和请求字节的普通 writer，记录 ObserveHTTP。
Non-goals: 范围限于本卡涉及的契约，保持公开签名和依赖。
Files: `middleware/httpmetrics/capture_regression_test.go`。
Tests: 观察者收到有效字节数、实际状态且仅调用一次。
Docs Sync: 核对新增验收与已有模块说明一致，记录证据和限制。
Done Definition: 完成以下验收，记录命令、结果与剩余限制。
Outcome: 底层接受 2 个字节并返回写错误，指标只观察实际字节和首次状态一次。

## 证据与影响

证据状态为补证。第 9 节检查号为 23,24,39,44,46。 [质量规则第 9 节](../../../docs/operations/agent-code-quality-rules.md#L267)

HTTP 指标包裹传输 recorder，部分写入时需要证明测量真实成功字节和首次状态。 [middleware/httpmetrics/http_metrics.go:21](../../../middleware/httpmetrics/http_metrics.go#L21)

[复审报告](../../../docs/evidence/stable-quality-review-c3a77410.md)

## 验收与验证

- [x] 观察者收到有效字节数、实际状态且仅调用一次。
- [x] 已有测试覆盖相同触发和断言时可直接复用，记录具体用例。

```sh
go test -race -timeout 60s ./middleware/httpmetrics
```

## 实施证据

- [TestHTTPMetricsCountsOnlyBytesAcceptedByWriter](../../../middleware/httpmetrics/http_metrics_test.go#L188)
