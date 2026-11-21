# Card 2003 — 核对外部 Manager 的直接检查仍使用兼容回退

Milestone: 稳定模块质量复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: x/observability/ops
Owned Files: `x/observability/ops/healthhttp/metrics_test.go`
Depends On: 1954

Goal: 错误原样返回，现行回退统计和不存在状态的处理得到明确验证。
Scope: 注入普通消费者实现，对正常状态、错误和无组件快照组合做直接调用。
Non-goals: 维持稳定公开签名与标准库依赖，将验证限于本卡范围。
Files: `x/observability/ops/healthhttp/metrics_test.go`。
Tests: 错误原样返回，现行回退统计和不存在状态的处理得到明确验证。
Docs Sync: 记录核验结论；只在现有契约文字需要澄清时另提文档变更。
Done Definition: 完成下列验收，记录验证命令、状态及剩余限制。
Outcome: 外部 Manager 仍沿用组件快照及返回错误，涵盖正常、诊断失败、degraded 与缺失。

## 证据与影响

第 9 节检查号为 6,11,31,42,44，当前证据状态为补证。 [质量规则第 9 节](../../../docs/operations/agent-code-quality-rules.md#L267)

外部 Manager 无法提供内置版本化检查结果，1954 的内部改动应保持该公开注入路径。 [x/observability/ops/healthhttp/tracker.go:228](../../../x/observability/ops/healthhttp/tracker.go#L228)

[复审报告](../../../docs/evidence/stable-quality-review-999d5e4d.md)

## 验收与验证

- [x] 错误原样返回，现行回退统计和不存在状态的处理得到明确验证。
- [x] 记录挂载入口、具体断言和结果。已有测试能覆盖时复用并注明。

```sh
go test -race -timeout 60s ./x/observability/ops/healthhttp
```

## 实施证据

- [TestTrackerExternalManagerDirectSnapshotFallback](../../../x/observability/ops/healthhttp/metrics_test.go#L515)
