# Card 2022 — 反查路径的字面查询分隔符往返

Milestone: 稳定模块第 9 节复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P3
State: done
Primary Module: router
Owned Files: `router/reverse_routing_group_test.go`
Depends On: 无

Goal: 反查路径的字面查询分隔符往返
Scope: 用末段含普通问号或井号的静态标签，从 URL 反查结果发请求。
Non-goals: 范围限于本卡涉及的契约，保持公开签名和依赖。
Files: `router/reverse_routing_group_test.go`。
Tests: Request.URL 无误解的 query 或 fragment，匹配原命名路由。
Docs Sync: 核对新增验收与已有模块说明一致，记录证据和限制。
Done Definition: 完成以下验收，记录命令、结果与剩余限制。
Outcome: 命名路由反查转义字面问号和井号，实际分派到该路由，URL 的 query 与 fragment 为空。

## 证据与影响

证据状态为补证。第 9 节检查号为 6,18,37,45。 [质量规则第 9 节](../../../docs/operations/agent-code-quality-rules.md#L267)

命名路由的静态段经 PathEscape，应用传回 URL 时仍应按原字面匹配。 [router/metadata.go:124](../../../router/metadata.go#L124)

[复审报告](../../../docs/evidence/stable-quality-review-c3a77410.md)

## 验收与验证

- [x] Request.URL 无误解的 query 或 fragment，匹配原命名路由。
- [x] 已有测试覆盖相同触发和断言时可直接复用，记录具体用例。

```sh
go test -race -timeout 60s ./router
```

## 实施证据

- [TestReverseStaticPathPreservesLiteralURLDelimiters](../../../router/reverse_routing_group_test.go#L492)
