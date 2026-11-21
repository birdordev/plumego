# Card 1887 — 补证幂等完成入口对边界到期与状态的处理顺序

Milestone: 稳定模块质量复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: store
Owned Files: `store/idempotency/store_test.go`
Depends On: 无

Goal: errors.Is 分类和时间边界明确，记录原值可继续供调用方使用。
Scope: 使用合成时间与普通哈希占位，逐个验证在之前、相等、之后的已完成与进行中记录。
Non-goals: 维持稳定公开签名与标准库依赖，将验证限于本卡范围。
Files: `store/idempotency/store_test.go`。
Tests: errors.Is 分类和时间边界明确，记录原值可继续供调用方使用。
Docs Sync: 记录核验结论；只在现有契约文字需要澄清时另提文档变更。
Done Definition: 完成下列验收，记录验证命令、状态及剩余限制。
Outcome: 合成时钟验证恰好过期时按 ErrExpired 分类，请求标识不匹配仍先报，状态分支仅作用于未过期记录。

## 证据与影响

第 9 节检查号为 6,14,22,31,41。证据状态为补证。

完成校验把时间、请求哈希和状态组合。需同一时钟矩阵确认恰在过期时的分类和已完成先后关系。 [store/idempotency/store.go:135](../../../store/idempotency/store.go#L135)

[复审报告](../../../docs/evidence/stable-quality-review-a8c2f28a.md)

## 验收与验证

- [x] errors.Is 分类和时间边界明确，记录原值可继续供调用方使用。
- [x] 记录挂载入口、具体断言和结果。已有测试能覆盖时复用并注明。

```sh
go test -race -timeout 60s ./store/idempotency
```

## 实施证据

- [TestCompletionExpirationAndStatusUseSuppliedClock](../../../store/idempotency/store_test.go#L366)

[批次验证与基准](../review-a8c2f28a/README.md)
