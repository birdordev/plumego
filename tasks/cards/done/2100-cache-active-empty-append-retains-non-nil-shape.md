# Card 2100 — 已有显式空值追加空输入保持形状

Milestone: 稳定模块第 9 节复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: store/cache
Owned Files: `store/cache/cache_test.go`
Depends On: 2067

Goal: 已有显式空值追加空输入保持形状
Scope: 通过 Set 放入非 nil 空切片，追加 nil 和非 nil 空输入后检查 TTL、计量和形状。
Non-goals: 范围限于本卡涉及的契约，保持公开签名和依赖。
Files: `store/cache/cache_test.go`。
Tests: 仍为非 nil 空切片并继续使用原 TTL。
Docs Sync: 核对新增验收与已有模块说明一致，记录证据和限制。
Done Definition: 完成以下验收，记录命令、结果与剩余限制。
Outcome: 并排对活跃非 nil 空切片追加 nil 和显式空切片，原形状、计量及期限不变。

## 证据与影响

证据状态为补证。第 9 节检查号为 6,31,34,41,45。 [质量规则第 9 节](../../../docs/operations/agent-code-quality-rules.md#L267)

活跃值的空追加短路，区别新键归一化。 [store/cache/memory.go:429](../../../store/cache/memory.go#L429)

[复审报告](../../../docs/evidence/stable-quality-review-2f962d21.md)

## 验收与验证

- [x] 仍为非 nil 空切片并继续使用原 TTL。
- [x] 已有测试覆盖相同触发和断言时可直接复用，记录具体用例。

```sh
go test -race -timeout 60s ./store/cache
```

## 实施证据

- [TestEmptyAppendKeepsActiveExplicitEmptyValue](../../../store/cache/cache_test.go#L1611)
