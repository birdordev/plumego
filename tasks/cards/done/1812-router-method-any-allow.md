# Card 1812 — 核对显式方法、Any 与 HEAD 的 Allow 边界

Milestone: 稳定模块质量复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: router
Owned Files: `router/router_contract_test.go`
Depends On: 无

Goal: 按优先级选择完整命中，405 的 Allow 不出现不适用方法。
Scope: 覆盖冻结前后有名静态路由和参数化 Any，不依赖 map 顺序。
Non-goals: 维持稳定公开签名与标准库依赖，将验证限于本卡范围。
Files: `router/router_contract_test.go`。
Tests: 按优先级选择完整命中，405 的 Allow 不出现不适用方法。
Docs Sync: 复核本卡涉及的已公开约定。
Done Definition: 完成下列验收，记录验证命令、状态及剩余限制。
Outcome: Any、显式方法和 HEAD 的 Allow 结果按匹配分支处理。

## 证据与影响

第 9 节检查号为 6,18,37,45。证据状态为补证。

主分派与 Allow 查询分别走方法匹配。需在同一 fixture 混合完全匹配与落空分支。 [router/dispatch.go:324](../../../router/dispatch.go#L324)

[复审报告](../../../docs/evidence/stable-quality-review-a074eadb.md)

## 验收与验证

- [x] 按优先级选择完整命中，405 的 Allow 不出现不适用方法。
- [x] 记录运行结果。若已有用例覆盖目标，记录用例及相关断言。

```sh
go test -race -timeout 60s ./router
```

## 实施证据

- [TestAnyFallbackDoesNotPolluteAllowForUnrelatedBranches](../../../router/router_contract_test.go#L1547)

[批次验证与基准](../review-a074eadb/README.md)
