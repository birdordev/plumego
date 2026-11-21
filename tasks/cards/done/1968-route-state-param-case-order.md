# Card 1968 — 核对 RouteState 对仅大小写不同参数键的快照

Milestone: 稳定模块质量复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P3
State: done
Primary Module: contract
Owned Files: `contract/context_extended_test.go`
Depends On: 无

Goal: 各访问器保持一致，回收状态后旧上下文内容稳定。
Scope: 加入两种大小写的普通键，并从上下文直接读取及取快照。
Non-goals: 维持稳定公开签名与标准库依赖，将验证限于本卡范围。
Files: `contract/context_extended_test.go`。
Tests: 各访问器保持一致，回收状态后旧上下文内容稳定。
Docs Sync: 记录核验结论；只在现有契约文字需要澄清时另提文档变更。
Done Definition: 完成下列验收，记录验证命令、状态及剩余限制。
Outcome: 参数名区分大小写，回收 RouteState 后派生 Context 仍保留两组独立参数。

## 证据与影响

第 9 节检查号为 12,25,31,38，当前证据状态为补证。 [质量规则第 9 节](../../../docs/operations/agent-code-quality-rules.md#L267)

RouteState 将参数存入紧凑区域，公开读取匹配 name。大小写不同的两个参数是否各自保持独立需要明确。 [contract/context_core.go:45](../../../contract/context_core.go#L45)

[复审报告](../../../docs/evidence/stable-quality-review-999d5e4d.md)

## 验收与验证

- [x] 各访问器保持一致，回收状态后旧上下文内容稳定。
- [x] 记录挂载入口、具体断言和结果。已有测试能覆盖时复用并注明。

```sh
go test -race -timeout 60s ./contract
```

## 实施证据

- [TestRouteStateSnapshotKeepsCaseDistinctParameters](../../../contract/context_extended_test.go#L65)
