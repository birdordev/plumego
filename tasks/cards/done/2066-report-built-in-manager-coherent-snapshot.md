# Card 2066 — 健康报告可能同时列出两个注册世代

Milestone: 稳定模块第 9 节复审
Recipe: specs/change-recipes/fix-bug.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: x/observability/ops
Owned Files: `x/observability/ops/healthhttp/tracker.go`, `x/observability/ops/healthhttp/manager.go`, `x/observability/ops/healthhttp/metrics_test.go`, `docs/modules/x/observability/ops/README.md`
Depends On: 无

Goal: 健康报告可能同时列出两个注册世代
Scope: 为内置 manager 提供私有报告快照，在同一读临界区复制整体状态及组件名，并按现有锁顺序取就绪发布值。外部 Manager 继续按已有接口读取，并说明其分次读限制。
Non-goals: 范围限于本卡涉及的契约，保持公开签名和依赖。
Files: `x/observability/ops/healthhttp/tracker.go`, `x/observability/ops/healthhttp/manager.go`, `x/observability/ops/healthhttp/metrics_test.go`, `docs/modules/x/observability/ops/README.md`。
Tests: 使用组件替换时序验证内置报告组件名与整体依赖来自同一注册表快照。保留既有就绪快照、关闭时的报告形状和第三方 Manager 回退。
Docs Sync: 已同步[所属模块说明](../../../docs/modules/x/observability/ops/README.md)，网站生成数据核对通过。
Done Definition: 完成以下验收，记录命令、结果与剩余限制。
Outcome: 完成。内置管理器的组件名与整体依赖共用一个注册表快照，并在既有锁顺序下读取就绪。外部 Manager 保留分次读取。

## 证据与影响

证据状态为已复现。第 9 节检查号为 16,32,34,42,45。 [质量规则第 9 节](../../../docs/operations/agent-code-quality-rules.md#L267)

两轮均用受控 Manager 在第一次读取后注销组件，报告同时得到旧 Components 和无该组件的整体快照。内置路径也按三个公共读方法分别上锁，实际并发出现频率未测。 [x/observability/ops/healthhttp/tracker.go:562](../../../x/observability/ops/healthhttp/tracker.go#L562)

[复审报告](../../../docs/evidence/stable-quality-review-2f962d21.md)

## 验收与验证

- [x] 使用组件替换时序验证内置报告组件名与整体依赖来自同一注册表快照。保留既有就绪快照、关闭时的报告形状和第三方 Manager 回退。
- [x] 已有测试覆盖相同触发和断言时可直接复用，记录具体用例。

```sh
go test -race -timeout 60s ./x/observability/ops/healthhttp
```

## 实施证据

- [TestBuiltInReportKeepsRegistryAndDependenciesCoherent](../../../x/observability/ops/healthhttp/metrics_test.go#L573)
- [TestReportPreservesExternalManagerSnapshotReads](../../../x/observability/ops/healthhttp/metrics_test.go#L674)

同一回归在 2f962d21 的原实现上失败，修复后通过。
