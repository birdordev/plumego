# Card 1896 — 核对三路统计在同名饱和后的聚合顺序

Milestone: 稳定模块质量复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: metrics
Owned Files: `metrics/multi_test.go`
Depends On: 无

Goal: 同名饱和后保持单调，其余名称仍精确，各顺序得到相同汇总。
Scope: 构造三个合法非负子统计，交换排列并读取快照，核对高位同名及两个小名称。
Non-goals: 维持稳定公开签名与标准库依赖，将验证限于本卡范围。
Files: `metrics/multi_test.go`。
Tests: 同名饱和后保持单调，其余名称仍精确，各顺序得到相同汇总。
Docs Sync: 记录核验结论；只在现有契约文字需要澄清时另提文档变更。
Done Definition: 完成下列验收，记录验证命令、状态及剩余限制。
Outcome: 三路合法子统计对同名项保持累加饱和，独立名称精确，顺序不影响结果。

## 证据与影响

第 9 节检查号为 6,32,34,44,46。证据状态为补证。

现有回归覆盖两个子统计在饱和上下界及独立名。需验证第三个子统计在饱和后继续提供同名及别的名称。 [metrics/multi.go:101](../../../metrics/multi.go#L101)

[复审报告](../../../docs/evidence/stable-quality-review-a8c2f28a.md)

## 验收与验证

- [x] 同名饱和后保持单调，其余名称仍精确，各顺序得到相同汇总。
- [x] 记录挂载入口、具体断言和结果。已有测试能覆盖时复用并注明。

```sh
go test -race -timeout 60s ./metrics
```

## 实施证据

- [TestThreeWayAggregationRetainsOtherNamesAfterSaturation](../../../metrics/multi_test.go#L565)

[批次验证与基准](../review-a8c2f28a/README.md)
