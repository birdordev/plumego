# Card 1785 — 锁定幂等完成校验在同一时刻的错误顺序

Milestone: 稳定模块质量复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: store
Owned Files: `store/idempotency/store_test.go`
Depends On: 无

Goal: 返回与当前契约相符，可用 errors.Is，未暴露哈希值。
Scope: 用确定时间验证已完成、恰好过期和哈希不符的组合。
Non-goals: 维持稳定公开签名与标准库依赖，将验证限于本卡范围。
Files: `store/idempotency/store_test.go`。
Tests: 返回与当前契约相符，可用 errors.Is，未暴露哈希值。
Docs Sync: 复核本卡涉及的已公开约定。
Done Definition: 完成下列验收，记录验证命令、状态及剩余限制。
Outcome: 明确哈希不匹配、过期和已完成同时存在时的顺序，保留稳定分类而无哈希文本。

## 证据与影响

第 9 节检查号为 6,22,28,41。证据状态为补证。

完成校验一次检查记录字段、哈希、过期和完成状态；多个条件同现时选择顺序会影响后端映射。 [store/idempotency/store.go:135](../../../store/idempotency/store.go#L135)

[复审报告](../../../docs/evidence/stable-quality-review-d224acbc.md)

## 验收与验证

- [x] 返回与当前契约相符，可用 errors.Is，未暴露哈希值。
- [x] 记录运行结果。若已有用例覆盖目标，记录用例及相关断言。

```sh
go test -race -timeout 60s ./store/idempotency
```

## 实施证据

- [TestValidateCompletionAtChecksMismatchThenExpiryThenStatus](../../../store/idempotency/store_test.go#L334)

[批次验证与基准](../review-d224acbc/README.md)
