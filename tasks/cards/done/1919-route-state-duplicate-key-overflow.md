# Card 1919 — 核对 RouteState 从内联到溢出后的参数覆盖

Milestone: 稳定模块质量复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: contract
Owned Files: `contract/context_test.go`
Depends On: 无

Goal: 重复处理遵从当前公开规则，每个可见键的两个读入口一致，快照不污染原状态。
Scope: 通过公开 RouteState API 添加重复键、达到内联上限并新增键，比较单值 accessor 与完整快照。
Non-goals: 维持稳定公开签名与标准库依赖，将验证限于本卡范围。
Files: `contract/context_test.go`。
Tests: 重复处理遵从当前公开规则，每个可见键的两个读入口一致，快照不污染原状态。
Docs Sync: 记录核验结论；只在现有契约文字需要澄清时另提文档变更。
Done Definition: 完成下列验收，记录验证命令、状态及剩余限制。
Outcome: RouteState 的重复参数采用最后写值，内联和溢出阶段的单值读取与快照一致。

## 证据与影响

第 9 节检查号为 6,27,34,35,38。证据状态为补证。

内联参数与溢出 map 使用不同代码。对中间重复键的写入、后续溢出和所有权应有明确证据。 [contract/context_core.go:45](../../../contract/context_core.go#L45)

[复审报告](../../../docs/evidence/stable-quality-review-2b94cf86.md)

## 验收与验证

- [x] 重复处理遵从当前公开规则，每个可见键的两个读入口一致，快照不污染原状态。
- [x] 记录挂载入口、具体断言和结果。已有测试能覆盖时复用并注明。

```sh
go test -race -timeout 60s ./contract
```

## 实施证据

- [TestRouteStateDuplicatesUseLastValueAcrossOverflow](../../../contract/context_test.go#L226)

[批次验证与基准](../review-2b94cf86/README.md)
