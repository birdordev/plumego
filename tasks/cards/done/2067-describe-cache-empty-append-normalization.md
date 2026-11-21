# Card 2067 — 说明空切片 Append 的归一化

Milestone: 稳定模块第 9 节复审
Recipe: specs/change-recipes/fix-bug.yaml
Context Package: implementation
Priority: P3
State: done
Primary Module: store/cache
Owned Files: `store/cache/memory.go`, `store/cache/cache_test.go`
Depends On: 无

Goal: 说明空切片 Append 的归一化
Scope: 在 Append 的 GoDoc 明确空追加在缺失或已过期键上创建 nil 值，在活跃键上保持原值。沿用当前默认 TTL 与可见形状。
Non-goals: 范围限于本卡涉及的契约，保持公开签名和依赖。
Files: `store/cache/memory.go`, `store/cache/cache_test.go`。
Tests: 复用并补充 nil 和显式空值组合，从 Append、Get、Set 公开入口确认形状、计量及兼容。
Docs Sync: 本卡同步所属公开入口或字段 GoDoc。
Done Definition: 完成以下验收，记录命令、结果与剩余限制。
Outcome: 完成。GoDoc 明确新键空追加是 nil，以及活跃值空追加保留原值和期限。复用原有公开兼容回归。

## 证据与影响

证据状态为源码确认。第 9 节检查号为 6,14,16,34,41。 [质量规则第 9 节](../../../docs/operations/agent-code-quality-rules.md#L267)

现有回归要求缺失键接收显式空切片时创建 nil 值。公开 Append 说明以 data 为值，容易使使用者类推 Set 保留显式空切片的行为。公开调用对照确认两者返回的形状不同。 [store/cache/memory.go:389](../../../store/cache/memory.go#L389)

[复审报告](../../../docs/evidence/stable-quality-review-2f962d21.md)

## 验收与验证

- [x] 复用并补充 nil 和显式空值组合，从 Append、Get、Set 公开入口确认形状、计量及兼容。
- [x] 已有测试覆盖相同触发和断言时可直接复用，记录具体用例。

```sh
go test -race -timeout 60s ./store/cache
```

## 实施证据

- [TestMemoryCacheEmptyAppend](../../../store/cache/cache_test.go#L1036)
- [TestMemoryCacheNilAndEmptyValueRoundTrip](../../../store/cache/cache_test.go#L661)
- [TestEmptyAppendKeepsActiveExplicitEmptyValue](../../../store/cache/cache_test.go#L1611)
