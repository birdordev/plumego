# Card 2001 — 核对根门面 Param 在 Prepare 后的路径上下文

Milestone: 稳定模块质量复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P3
State: done
Primary Module: .
Owned Files: `plumego_test.go`
Depends On: 无

Goal: 已登记参数和缺失参数行为一致，准备后入口正常。
Scope: 使用公开构造、嵌套 Group、Prepare 和 HTTP 请求，从 handler 调用 Param。
Non-goals: 维持稳定公开签名与标准库依赖，将验证限于本卡范围。
Files: `plumego_test.go`。
Tests: 已登记参数和缺失参数行为一致，准备后入口正常。
Docs Sync: 记录核验结论；只在现有契约文字需要澄清时另提文档变更。
Done Definition: 完成下列验收，记录验证命令、状态及剩余限制。
Outcome: 通过根门面创建 App、Prepare 后在嵌套组的 handler 读取已登记和缺失参数。

## 证据与影响

第 9 节检查号为 3,8,25,36,37，当前证据状态为补证。 [质量规则第 9 节](../../../docs/operations/agent-code-quality-rules.md#L267)

Param 将读取委托给稳定上下文，路由 matcher 负责发布。需在根门面装配的嵌套组上验证一次完整请求。 [plumego.go:73](../../../plumego.go#L73)

[复审报告](../../../docs/evidence/stable-quality-review-999d5e4d.md)

## 验收与验证

- [x] 已登记参数和缺失参数行为一致，准备后入口正常。
- [x] 记录挂载入口、具体断言和结果。已有测试能覆盖时复用并注明。

```sh
go test -race -timeout 60s .
```

## 实施证据

- [TestFacadeParamAfterPreparingNestedGroup](../../../plumego_test.go#L249)
