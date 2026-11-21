# Card 1984 — 核对缓存替换值的容量和到期计量

Milestone: 稳定模块质量复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: store/cache
Owned Files: `store/cache/state_regression_test.go`
Depends On: 无

Goal: 成功、失败、清理后的字节统计精确且旧值不因超限被替换。
Scope: 填充限额缓存，在同一键先替换到上限，再尝试超限值，观察时间边界。
Non-goals: 维持稳定公开签名与标准库依赖，将验证限于本卡范围。
Files: `store/cache/state_regression_test.go`。
Tests: 成功、失败、清理后的字节统计精确且旧值不因超限被替换。
Docs Sync: 记录核验结论；只在现有契约文字需要澄清时另提文档变更。
Done Definition: 完成下列验收，记录验证命令、状态及剩余限制。
Outcome: 替换恰好填满缓存，超限写入不修改值或 TTL。越过原截止后清零条目和字节计量。

## 证据与影响

第 9 节检查号为 31,34,35,41，当前证据状态为补证。 [质量规则第 9 节](../../../docs/operations/agent-code-quality-rules.md#L267)

容量判断会抵扣已有值，过期清理也会改变备选配额。需要记录替换恰到边界的正确计量。 [store/cache/memory.go:159](../../../store/cache/memory.go#L159)

[复审报告](../../../docs/evidence/stable-quality-review-999d5e4d.md)

## 验收与验证

- [x] 成功、失败、清理后的字节统计精确且旧值不因超限被替换。
- [x] 记录挂载入口、具体断言和结果。已有测试能覆盖时复用并注明。

```sh
go test -race -timeout 60s ./store/cache
```

## 实施证据

- [TestCacheReplacementAtCapacityKeepsExpiryAndAccounting](../../../store/cache/state_regression_test.go#L562)
