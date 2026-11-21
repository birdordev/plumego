# Card 2051 — 完成校验的精确到期点

Milestone: 稳定模块第 9 节复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: store/idempotency
Owned Files: `store/idempotency/store_test.go`
Depends On: 无

Goal: 完成校验的精确到期点
Scope: 用同一合法占位记录比较到期前一纳秒、恰在时间点与后一纳秒。
Non-goals: 范围限于本卡涉及的契约，保持公开签名和依赖。
Files: `store/idempotency/store_test.go`。
Tests: 分类在文档规定的边界切换，状态和原记录保持不变。
Docs Sync: 核对新增验收与已有模块说明一致，记录证据和限制。
Done Definition: 完成以下验收，记录命令、结果与剩余限制。
Outcome: 到期前、精确到期、到期后三点符合分类，原记录状态和期限不变。

## 证据与影响

证据状态为补证。第 9 节检查号为 14,22,31,41,46。 [质量规则第 9 节](../../../docs/operations/agent-code-quality-rules.md#L267)

显式 now 入口允许确定性核验有效窗口端点。 [store/idempotency/store.go:135](../../../store/idempotency/store.go#L135)

[复审报告](../../../docs/evidence/stable-quality-review-c3a77410.md)

## 验收与验证

- [x] 分类在文档规定的边界切换，状态和原记录保持不变。
- [x] 已有测试覆盖相同触发和断言时可直接复用，记录具体用例。

```sh
go test -race -timeout 60s ./store/idempotency
```

## 实施证据

- [TestCompletionExpirationAndStatusUseSuppliedClock](../../../store/idempotency/store_test.go#L366)
