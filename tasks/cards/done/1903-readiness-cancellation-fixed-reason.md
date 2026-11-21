# Card 1903 — 取消的空健康批次丢失就绪诊断原因

Milestone: 稳定模块质量复审
Recipe: specs/change-recipes/fix-bug.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: x/observability/ops
Owned Files: `x/observability/ops/healthhttp/manager.go`, `x/observability/ops/healthhttp/aggregate_regression_test.go`, `docs/modules/x/observability/ops/README.md`
Depends On: 1900

Goal: 分别覆盖预取消、截止、正常组件失败与空成功批，保留手工就绪和并发版本检查。
Scope: 优先给取消或截止分支使用固定分类文字；有失败组件时使用分类和名称，其余分支用确定的不可用原因。
Non-goals: 维持稳定公开签名与标准库依赖，将验证限于本卡范围。
Files: `x/observability/ops/healthhttp/manager.go`, `x/observability/ops/healthhttp/aggregate_regression_test.go`, `docs/modules/x/observability/ops/README.md`。
Tests: 分别覆盖预取消、截止、正常组件失败与空成功批，保留手工就绪和并发版本检查。
Docs Sync: 行为变更后同步清单对应的模块说明并校验生成数据。
Done Definition: 完成下列验收，记录验证命令、状态及剩余限制。
Outcome: 就绪原因区分取消、超时和组件失败，空批次正常完成时可就绪。取消导致未尝试的组件仍保有先前实际探测快照。

## 证据与影响

第 9 节检查号为 22,26,28,42,46。证据状态为已复现。

取消的空批次返回 message 为 context canceled，就绪拒绝原因却为 components failed: []。调用方无法区分上下文终止和检查失败。 [x/observability/ops/healthhttp/manager.go:402](../../../x/observability/ops/healthhttp/manager.go#L402)

[复审报告](../../../docs/evidence/stable-quality-review-2b94cf86.md)

## 验收与验证

- [x] 分别覆盖预取消、截止、正常组件失败与空成功批，保留手工就绪和并发版本检查。
- [x] 记录挂载入口、具体断言和结果。已有测试能覆盖时复用并注明。

```sh
go test -race -timeout 60s ./x/observability/ops/healthhttp
make website-sync
make website-check-generated
```

## 实施证据

- [TestBatchReadinessUsesCancellationReasonWithoutNewSample](../../../x/observability/ops/healthhttp/aggregate_regression_test.go#L226)

[批次验证与基准](../review-2b94cf86/README.md)
