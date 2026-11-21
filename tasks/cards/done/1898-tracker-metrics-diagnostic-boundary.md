# Card 1898 — Tracker 的可序列化统计保留原始依赖错误文本

Milestone: 稳定模块质量复审
Recipe: specs/change-recipes/fix-bug.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: x/observability/ops
Owned Files: `x/observability/ops/healthhttp/tracker.go`, `x/observability/ops/healthhttp/metrics_test.go`, `docs/modules/x/observability/ops/README.md`
Depends On: 无

Goal: 用无敏感数据的合成错误确认序列化字段只保留固定诊断，计数和调用者错误分类不变。
Scope: 在写入可公开序列化的记录前转为固定诊断或已允许的分类，调用者直接收到的原因链保持原契约。
Non-goals: 维持稳定公开签名与标准库依赖，将验证限于本卡范围。
Files: `x/observability/ops/healthhttp/tracker.go`, `x/observability/ops/healthhttp/metrics_test.go`, `docs/modules/x/observability/ops/README.md`。
Tests: 用无敏感数据的合成错误确认序列化字段只保留固定诊断，计数和调用者错误分类不变。
Docs Sync: 行为变更后同步清单对应的模块说明并校验生成数据。
Done Definition: 完成下列验收，记录验证命令、状态及剩余限制。
Outcome: 可序列化的组件失败统计使用固定诊断，直接调用返回的分类和原因不变。

## 证据与影响

第 9 节检查号为 22,25,38,42。证据状态为源码确认。

单组件检查把底层错误交给统计记录，原文进入可序列化 RecentHistory。普通健康响应已使用固定消息。这里能直接确认未经归一化的数据流，实际部署及真实敏感值未取证。 [x/observability/ops/healthhttp/tracker.go:361](../../../x/observability/ops/healthhttp/tracker.go#L361)

[复审报告](../../../docs/evidence/stable-quality-review-a8c2f28a.md)

## 验收与验证

- [x] 用无敏感数据的合成错误确认序列化字段只保留固定诊断，计数和调用者错误分类不变。
- [x] 记录挂载入口、具体断言和结果。已有测试能覆盖时复用并注明。

```sh
go test -race -timeout 60s ./x/observability/ops/healthhttp
make website-sync
make website-check-generated
```

## 实施证据

- [TestAcceptanceTrackerMetricsUsesFixedPublicDiagnostic](../../../x/observability/ops/healthhttp/metrics_test.go#L290)

[批次验证与基准](../review-a8c2f28a/README.md)
