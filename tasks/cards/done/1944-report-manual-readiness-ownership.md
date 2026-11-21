# Card 1944 — 核对手工就绪决定与自然探测报告的关系

Milestone: 稳定模块质量复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: x/observability/ops
Owned Files: `x/observability/ops/healthhttp/metrics_test.go`
Depends On: 1900

Goal: 报告能描述健康和显式运维决定，版本护栏保留，下一批更新有明确归属。
Scope: 在成功与失败组件的合成场景分别 MarkReady 或 MarkNotReady，再读取报告与下一次自然检查。
Non-goals: 维持稳定公开签名与标准库依赖，将验证限于本卡范围。
Files: `x/observability/ops/healthhttp/metrics_test.go`。
Tests: 报告能描述健康和显式运维决定，版本护栏保留，下一批更新有明确归属。
Docs Sync: 记录核验结论；只在现有契约文字需要澄清时另提文档变更。
Done Definition: 完成下列验收，记录验证命令、状态及剩余限制。
Outcome: 手工就绪和真实组件健康是独立输入。下一次新批次仍可更新 readiness 决定。

## 证据与影响

第 9 节检查号为 6,28,31,42。证据状态为补证。

报告分别读取 HealthStatus 和 Readiness。手工就绪是单独决策，修复自动失败后需防止两层字段被无条件合并。 [x/observability/ops/healthhttp/tracker.go:529](../../../x/observability/ops/healthhttp/tracker.go#L529)

[复审报告](../../../docs/evidence/stable-quality-review-2b94cf86.md)

## 验收与验证

- [x] 报告能描述健康和显式运维决定，版本护栏保留，下一批更新有明确归属。
- [x] 记录挂载入口、具体断言和结果。已有测试能覆盖时复用并注明。

```sh
go test -race -timeout 60s ./x/observability/ops/healthhttp
```

## 实施证据

- [TestManualReadinessAndObservedComponentHealthStayIndependent](../../../x/observability/ops/healthhttp/metrics_test.go#L394)

[批次验证与基准](../review-2b94cf86/README.md)
