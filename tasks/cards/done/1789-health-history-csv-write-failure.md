# Card 1789 — 核对历史 CSV 写失败后的响应终态

Milestone: 稳定模块质量复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: x/observability
Owned Files: `x/observability/ops/healthhttp/history_test.go`
Depends On: 无

Goal: 只尝试原 CSV 响应，错误阶段与可观测性明确。
Scope: 使用受控只允许部分写入的 writer 验证响应计数。
Non-goals: 维持稳定公开签名与标准库依赖，将验证限于本卡范围。
Files: `x/observability/ops/healthhttp/history_test.go`。
Tests: 只尝试原 CSV 响应，错误阶段与可观测性明确。
Docs Sync: 复核本卡涉及的已公开约定。
Done Definition: 完成下列验收，记录验证命令、状态及剩余限制。
Outcome: 底层 CSV writer 失败时只提交一次 CSV 响应，保持 Content-Type，无附加 JSON。

## 证据与影响

第 9 节检查号为 22,23,29,42。证据状态为补证。

CSV writer 有显式 flush，需确认关闭或短写时不会输出第二种格式。 [x/observability/ops/healthhttp/history.go:140](../../../x/observability/ops/healthhttp/history.go#L140)

[复审报告](../../../docs/evidence/stable-quality-review-d224acbc.md)

## 验收与验证

- [x] 只尝试原 CSV 响应，错误阶段与可观测性明确。
- [x] 记录运行结果。若已有用例覆盖目标，记录用例及相关断言。

```sh
go test -race -timeout 60s ./x/observability/ops/healthhttp
```

## 实施证据

- [TestHistoryCSVExportStopsAfterFailedWrite](../../../x/observability/ops/healthhttp/history_test.go#L240)

[批次验证与基准](../review-d224acbc/README.md)
