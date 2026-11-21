# Card 1962 — 核对重叠路由的 Allow 中 HEAD 只出现一次

Milestone: 稳定模块质量复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: router
Owned Files: `router/router_contract_test.go`
Depends On: 无

Goal: Allow 方法值去重且排序稳定，显式 HEAD 仍命中自己的 handler。
Scope: 注册不同方法的合成重叠路由，再用未登记方法请求。
Non-goals: 维持稳定公开签名与标准库依赖，将验证限于本卡范围。
Files: `router/router_contract_test.go`。
Tests: Allow 方法值去重且排序稳定，显式 HEAD 仍命中自己的 handler。
Docs Sync: 记录核验结论；只在现有契约文字需要澄清时另提文档变更。
Done Definition: 完成下列验收，记录验证命令、状态及剩余限制。
Outcome: 重叠的静态、通配、参数路由返回去重排序的 Allow。显式 HEAD 在重复请求中使用自己的处理器。

## 证据与影响

第 9 节检查号为 6,13,37,45，当前证据状态为补证。 [质量规则第 9 节](../../../docs/operations/agent-code-quality-rules.md#L267)

Allow 从可匹配方法收集并补齐隐式 HEAD。同一路径可被静态、通配及显式 HEAD 候选命中。 [router/dispatch.go:367](../../../router/dispatch.go#L367)

[复审报告](../../../docs/evidence/stable-quality-review-999d5e4d.md)

## 验收与验证

- [x] Allow 方法值去重且排序稳定，显式 HEAD 仍命中自己的 handler。
- [x] 记录挂载入口、具体断言和结果。已有测试能覆盖时复用并注明。

```sh
go test -race -timeout 60s ./router
```

## 实施证据

- [TestAllowMethodsAcrossOverlapWithExplicitHead](../../../router/router_contract_test.go#L1710)
