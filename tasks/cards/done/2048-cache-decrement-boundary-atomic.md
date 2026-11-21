# Card 2048 — 缓存 Decr 边界错误保留值和 TTL

Milestone: 稳定模块第 9 节复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: store/cache
Owned Files: `store/cache/state_regression_test.go`
Depends On: 无

Goal: 缓存 Decr 边界错误保留值和 TTL
Scope: 在可控时钟写最小整数并测试再减一，从公开入口读取并到期。
Non-goals: 范围限于本卡涉及的契约，保持公开签名和依赖。
Files: `store/cache/state_regression_test.go`。
Tests: 溢出被分类，字节值和过期点保持原样。
Docs Sync: 核对新增验收与已有模块说明一致，记录证据和限制。
Done Definition: 完成以下验收，记录命令、结果与剩余限制。
Outcome: 尝试低于最小 int64 的减法返回分类错误，原字节和精确过期点保持。

## 证据与影响

证据状态为补证。第 9 节检查号为 22,28,31,41,46。 [质量规则第 9 节](../../../docs/operations/agent-code-quality-rules.md#L267)

Decr 共用 updateInteger，算术错误发生时还应保留既有过期状态。 [store/cache/memory.go:574](../../../store/cache/memory.go#L574)

[复审报告](../../../docs/evidence/stable-quality-review-c3a77410.md)

## 验收与验证

- [x] 溢出被分类，字节值和过期点保持原样。
- [x] 已有测试覆盖相同触发和断言时可直接复用，记录具体用例。

```sh
go test -race -timeout 60s ./store/cache
```

## 实施证据

- [TestDecrBelowMinimumRetainsValueAndDeadline](../../../store/cache/state_regression_test.go#L639)
