# Card 2010 — 基础统计越上界后返回负值

Milestone: 稳定模块第 9 节复审
Recipe: specs/change-recipes/fix-bug.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: metrics
Owned Files: `metrics/collector.go`, `metrics/multi_test.go`, `docs/modules/metrics/README.md`
Depends On: 无

Goal: 基础统计越上界后返回负值
Scope: 在同一锁内为三处计数复用聚合的饱和相加，保留 Clear 和状态分类。
Non-goals: 范围限于本卡涉及的契约，保持公开签名和依赖。
Files: `metrics/collector.go`, `metrics/multi_test.go`, `docs/modules/metrics/README.md`。
Tests: 验证上限前后非负、错误数不超过总数、另外一个名字仍可增长，清空后重新从零计数。
Docs Sync: 已同步[所属模块说明](../../../docs/modules/metrics/README.md)，网站生成数据核对通过。
Done Definition: 完成以下验收，记录命令、结果与剩余限制。
Outcome: 已修复。基础计数达到上限后饱和，其他名称仍增长，清空后恢复累计。直接及聚合统计都通过。

## 证据与影响

证据状态为已复现。第 9 节检查号为 6,32,44,45。 [质量规则第 9 节](../../../docs/operations/agent-code-quality-rules.md#L267)

将同包测试计数预置到 int64 上限，再用公开 Record 记录一项失败。直接和聚合统计的总数、错误数与名称计数均回绕为最小负值，违反非负契约。生产到达上界的概率未取证。 [metrics/collector.go:150](../../../metrics/collector.go#L150)

[复审报告](../../../docs/evidence/stable-quality-review-c3a77410.md)

## 验收与验证

- [x] 验证上限前后非负、错误数不超过总数、另外一个名字仍可增长，清空后重新从零计数。
- [x] 已有测试覆盖相同触发和断言时可直接复用，记录具体用例。

```sh
go test -race -timeout 60s ./metrics
```

## 实施证据

- [TestBaseCountersSaturateDirectlyAndInMulti](../../../metrics/multi_test.go#L607)

同一回归在 c3a77410 的原实现上失败，修复后通过。
