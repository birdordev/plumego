# Card 2011 — 健康报告组件列表顺序漂移

Milestone: 稳定模块第 9 节复审
Recipe: specs/change-recipes/fix-bug.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: x/observability/ops
Owned Files: `x/observability/ops/healthhttp/tracker.go`, `x/observability/ops/healthhttp/metrics_test.go`, `docs/modules/x/observability/ops/README.md`
Depends On: 无

Goal: 健康报告组件列表顺序漂移
Scope: 在报告组装处排序组件名，与健康批次的组件顺序对齐。公开字段形状保持原样。
Non-goals: 范围限于本卡涉及的契约，保持公开签名和依赖。
Files: `x/observability/ops/healthhttp/tracker.go`, `x/observability/ops/healthhttp/metrics_test.go`, `docs/modules/x/observability/ops/README.md`。
Tests: 对乱序注册或快照输入，验证稳定字典序及空集合的序列化形状。
Docs Sync: 已同步[所属模块说明](../../../docs/modules/x/observability/ops/README.md)，网站生成数据核对通过。
Done Definition: 完成以下验收，记录命令、结果与剩余限制。
Outcome: 已修复。健康报告按字典序列出组件，内置管理器无组件时仍编码为空数组。

## 证据与影响

证据状态为已复现。第 9 节检查号为 6,16,34,42,45。 [质量规则第 9 节](../../../docs/operations/agent-code-quality-rules.md#L267)

固定组件集合和管理器快照后，连续 GenerateReport 返回不同组件列表排列。调用方按序列对比时出现无状态变化的差异。 [x/observability/ops/healthhttp/tracker.go:558](../../../x/observability/ops/healthhttp/tracker.go#L558)

[复审报告](../../../docs/evidence/stable-quality-review-c3a77410.md)

## 验收与验证

- [x] 对乱序注册或快照输入，验证稳定字典序及空集合的序列化形状。
- [x] 已有测试覆盖相同触发和断言时可直接复用，记录具体用例。

```sh
go test -race -timeout 60s ./x/observability/ops/healthhttp
```

## 实施证据

- [TestGenerateReportSortsComponentNames](../../../x/observability/ops/healthhttp/metrics_test.go#L542)

同一回归在 c3a77410 的原实现上失败，修复后通过。
