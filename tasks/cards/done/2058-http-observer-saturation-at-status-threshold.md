# Card 2058 — 饱和边界下 HTTP 状态分类仍独立

Milestone: 稳定模块第 9 节复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: metrics
Owned Files: `metrics/collector_test.go`
Depends On: 2010

Goal: 饱和边界下 HTTP 状态分类仍独立
Scope: 预置总计数与 HTTP 名称计数为上限，先调用 399、再调用 400 的 ObserveHTTP。
Non-goals: 范围限于本卡涉及的契约，保持公开签名和依赖。
Files: `metrics/collector_test.go`。
Tests: 总量和名称保持饱和，错误数只在 400 时增长一次，Clear 后 HTTP 入口仍可正常计数。
Docs Sync: 核对新增验收与已有模块说明一致，记录证据和限制。
Done Definition: 完成以下验收，记录命令、结果与剩余限制。
Outcome: HTTP 状态边界 399 与 400 的错误分类独立于总量饱和，清空后仍可记录。

## 证据与影响

证据状态为补证。第 9 节检查号为 32,44,45,46。 [质量规则第 9 节](../../../docs/operations/agent-code-quality-rules.md#L267)

2010 修改共享计数边界后，ObserveHTTP 仍需保留 399 与 400 的错误分类差异。 [metrics/collector.go:163](../../../metrics/collector.go#L163)

[复审报告](../../../docs/evidence/stable-quality-review-c3a77410.md)

## 验收与验证

- [x] 总量和名称保持饱和，错误数只在 400 时增长一次，Clear 后 HTTP 入口仍可正常计数。
- [x] 已有测试覆盖相同触发和断言时可直接复用，记录具体用例。

```sh
go test -race -timeout 60s ./metrics
```

## 实施证据

- [TestBaseHTTPStatusCountersAtSaturation](../../../metrics/collector_test.go#L324)
