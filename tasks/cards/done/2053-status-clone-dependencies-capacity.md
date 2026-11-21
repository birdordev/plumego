# Card 2053 — 健康依赖列表改写与扩容隔离

Milestone: 稳定模块第 9 节复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P3
State: done
Primary Module: health
Owned Files: `health/health_test.go`
Depends On: 无

Goal: 健康依赖列表改写与扩容隔离
Scope: 克隆带额外容量的依赖列表，替换、追加后核验原列表及其备用容量。
Non-goals: 范围限于本卡涉及的契约，保持公开签名和依赖。
Files: `health/health_test.go`。
Tests: 调用方修改不更改原列表的元素，可选字段形状符合契约。
Docs Sync: 核对新增验收与已有模块说明一致，记录证据和限制。
Done Definition: 完成以下验收，记录命令、结果与剩余限制。
Outcome: 替换并追加克隆后的依赖切片时，原切片与原底层备用位置均保持。

## 证据与影响

证据状态为补证。第 9 节检查号为 34,42,45。 [质量规则第 9 节](../../../docs/operations/agent-code-quality-rules.md#L267)

Clone 使用 slices.Clone，依赖列表的增删与底层共享需要通过非零剩余容量确认。 [health/core.go:48](../../../health/core.go#L48)

[复审报告](../../../docs/evidence/stable-quality-review-c3a77410.md)

## 验收与验证

- [x] 调用方修改不更改原列表的元素，可选字段形状符合契约。
- [x] 已有测试覆盖相同触发和断言时可直接复用，记录具体用例。

```sh
go test -race -timeout 60s ./health
```

## 实施证据

- [TestHealthCloneDependenciesDoNotReuseSpareCapacity](../../../health/health_test.go#L242)
