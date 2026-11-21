# Card 1912 — 核对重复命名失败后的热分派与元数据

Milestone: 稳定模块质量复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: router
Owned Files: `router/router_contract_test.go`
Depends On: 无

Goal: 原参数与命名路由仍生效，未出现失败的静态分支或名称变化。
Scope: 先缓存动态结果，再尝试同名的更具体静态路由，检查 URL 和 Routes，冻结后重试访问。
Non-goals: 维持稳定公开签名与标准库依赖，将验证限于本卡范围。
Files: `router/router_contract_test.go`。
Tests: 原参数与命名路由仍生效，未出现失败的静态分支或名称变化。
Docs Sync: 记录核验结论；只在现有契约文字需要澄清时另提文档变更。
Done Definition: 完成下列验收，记录验证命令、状态及剩余限制。
Outcome: 重复路由名称的静态注册失败后，热动态路径、冷查找、冻结和反向 URL 仍一致。

## 证据与影响

第 9 节检查号为 19,31,32,37。证据状态为补证。

命名重复检查先于路由树变更。需在预热的动态路径旁注册冲突的静态前缀，检查失败后冷、热分派没有被污染。 [router/registration.go:102](../../../router/registration.go#L102)

[复审报告](../../../docs/evidence/stable-quality-review-2b94cf86.md)

## 验收与验证

- [x] 原参数与命名路由仍生效，未出现失败的静态分支或名称变化。
- [x] 记录挂载入口、具体断言和结果。已有测试能覆盖时复用并注明。

```sh
go test -race -timeout 60s ./router
```

## 实施证据

- [TestDuplicateRouteNameLeavesHotDynamicMatchAndMetadata](../../../router/router_contract_test.go#L1647)

[批次验证与基准](../review-2b94cf86/README.md)
