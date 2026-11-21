# Card 1965 — 核对反向路由参数不成对时的诊断

Milestone: 稳定模块质量复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P3
State: done
Primary Module: router
Owned Files: `router/reverse_routing_group_test.go`
Depends On: 无

Goal: 错误输入无部分 URL，下一次合法调用仍生成同样的 URL。
Scope: 调用公开 URL 两次，对比完整键值对与尾部孤立键的返回。
Non-goals: 维持稳定公开签名与标准库依赖，将验证限于本卡范围。
Files: `router/reverse_routing_group_test.go`。
Tests: 错误输入无部分 URL，下一次合法调用仍生成同样的 URL。
Docs Sync: 记录核验结论；只在现有契约文字需要澄清时另提文档变更。
Done Definition: 完成下列验收，记录验证命令、状态及剩余限制。
Outcome: 组命名路由拒绝不成对输入且不生成部分 URL，随后合法参数仍正确构造 URL。

## 证据与影响

第 9 节检查号为 12,15,37,46，当前证据状态为补证。 [质量规则第 9 节](../../../docs/operations/agent-code-quality-rules.md#L267)

反向路由分别验证名称和参数键值对。对命名组的一个必需参数与一个不成对输入尚需联合核对。 [router/metadata.go:59](../../../router/metadata.go#L59)

[复审报告](../../../docs/evidence/stable-quality-review-999d5e4d.md)

## 验收与验证

- [x] 错误输入无部分 URL，下一次合法调用仍生成同样的 URL。
- [x] 记录挂载入口、具体断言和结果。已有测试能覆盖时复用并注明。

```sh
go test -race -timeout 60s ./router
```

## 实施证据

- [TestNamedGroupURLRejectsUnpairedParameterThenRecovers](../../../router/reverse_routing_group_test.go#L481)
