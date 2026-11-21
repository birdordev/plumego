# Card 2104 — 幂等记录副本的时间字段和过期判定

Milestone: 稳定模块第 9 节复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P3
State: done
Primary Module: store/idempotency
Owned Files: `store/idempotency/store_test.go`
Depends On: 无

Goal: 幂等记录副本的时间字段和过期判定
Scope: 从一条占位记录克隆，给副本移动 ExpiresAt，同时改写副本响应，按同一 now 校验。
Non-goals: 范围限于本卡涉及的契约，保持公开签名和依赖。
Files: `store/idempotency/store_test.go`。
Tests: 原记录保持未过期且响应不变，副本按新期限返回 ErrExpired。
Docs Sync: 核对新增验收与已有模块说明一致，记录证据和限制。
Done Definition: 完成以下验收，记录命令、结果与剩余限制。
Outcome: 克隆后更新期限及响应，副本按新的到期点分类，原记录仍可继续完成且响应未改。

## 证据与影响

证据状态为补证。第 9 节检查号为 16,34,41,45。 [质量规则第 9 节](../../../docs/operations/agent-code-quality-rules.md#L267)

时间字段按值复制，响应体另做独立复制。核对改写副本期限后，实际完成校验对两份记录各用自己的状态。 [store/idempotency/store.go:93](../../../store/idempotency/store.go#L93)

[复审报告](../../../docs/evidence/stable-quality-review-2f962d21.md)

## 验收与验证

- [x] 原记录保持未过期且响应不变，副本按新期限返回 ErrExpired。
- [x] 已有测试覆盖相同触发和断言时可直接复用，记录具体用例。

```sh
go test -race -timeout 60s ./store/idempotency
```

## 实施证据

- [TestClonedRecordExpiryAndResponseCanChangeIndependently](../../../store/idempotency/store_test.go#L421)
