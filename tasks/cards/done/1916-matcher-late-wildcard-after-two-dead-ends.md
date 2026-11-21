# Card 1916 — 核对多层静态与参数落空后的通配参数归属

Milestone: 稳定模块质量复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: router
Owned Files: `router/router_contract_test.go`
Depends On: 无

Goal: 只返回通配分支的参数、模板和名称，405 的 Allow 不因死端出现。
Scope: 建静态和参数两个走不完整的分支，第三个通配成功；浅根和嵌套组各在冷热状态运行。
Non-goals: 维持稳定公开签名与标准库依赖，将验证限于本卡范围。
Files: `router/router_contract_test.go`。
Tests: 只返回通配分支的参数、模板和名称，405 的 Allow 不因死端出现。
Docs Sync: 记录核验结论；只在现有契约文字需要澄清时另提文档变更。
Done Definition: 完成下列验收，记录验证命令、状态及剩余限制。
Outcome: 同一个请求跨过静态和参数两条死端才捕获通配后缀，参数、名称、模板和 Allow 均按完整匹配。

## 证据与影响

第 9 节检查号为 29,34,37,46。证据状态为补证。

匹配器先静态后参数再通配，回退依赖参数回滚。需同时经过两次较深的死端后断言通配拿到原始后缀。 [router/matcher.go:34](../../../router/matcher.go#L34)

[复审报告](../../../docs/evidence/stable-quality-review-2b94cf86.md)

## 验收与验证

- [x] 只返回通配分支的参数、模板和名称，405 的 Allow 不因死端出现。
- [x] 记录挂载入口、具体断言和结果。已有测试能覆盖时复用并注明。

```sh
go test -race -timeout 60s ./router
```

## 实施证据

- [TestNestedWildcardWinsAfterBothMoreSpecificDeadEnds](../../../router/router_contract_test.go#L1685)

[批次验证与基准](../review-2b94cf86/README.md)
