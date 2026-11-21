# Card 2112 — 空名称记录在总量上界的计量

Milestone: 稳定模块第 9 节复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: metrics
Owned Files: `metrics/collector_test.go`
Depends On: 无

Goal: 空名称记录在总量上界的计量
Scope: 预置总量上限，记录一条无名显式普通错误和一条无名成功。
Non-goals: 范围限于本卡涉及的契约，保持公开签名和依赖。
Files: `metrics/collector_test.go`。
Tests: 总量饱和，错误增长一次，名称统计不增加。
Docs Sync: 核对新增验收与已有模块说明一致，记录证据和限制。
Done Definition: 完成以下验收，记录命令、结果与剩余限制。
Outcome: 总量饱和期间无名显式错误仍递增错误数，无名记录不会创建序列。

## 证据与影响

证据状态为补证。第 9 节检查号为 32,35,44,45。 [质量规则第 9 节](../../../docs/operations/agent-code-quality-rules.md#L267)

基础计数饱和修复已验证带名字路径，空名不应建立统计项。 [metrics/collector.go:145](../../../metrics/collector.go#L145)

[复审报告](../../../docs/evidence/stable-quality-review-2f962d21.md)

## 验收与验证

- [x] 总量饱和，错误增长一次，名称统计不增加。
- [x] 已有测试覆盖相同触发和断言时可直接复用，记录具体用例。

```sh
go test -race -timeout 60s ./metrics
```

## 实施证据

- [TestNamelessRecordsKeepIndependentErrorCountAtSaturation](../../../metrics/collector_test.go#L344)
