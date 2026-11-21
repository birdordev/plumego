# Card 1960 — 核对嵌套组空相对路径上的命名路由

Milestone: 稳定模块质量复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: core
Owned Files: `core/routing_test.go`
Depends On: 无

Goal: 完整前缀一致且命名可查询，添加失败时不出现孤立的名称。
Scope: 在嵌套组登记空相对路径和命名选项，由 HTTP 请求、URL 和路由元数据三处观察。
Non-goals: 维持稳定公开签名与标准库依赖，将验证限于本卡范围。
Files: `core/routing_test.go`。
Tests: 完整前缀一致且命名可查询，添加失败时不出现孤立的名称。
Docs Sync: 记录核验结论；只在现有契约文字需要澄清时另提文档变更。
Done Definition: 完成下列验收，记录验证命令、状态及剩余限制。
Outcome: 空相对路径命中嵌套组根，反向 URL 与匹配一致。显式斜杠重复注册被拒绝且不遗留第二个名字。

## 证据与影响

第 9 节检查号为 8,12,36,37，当前证据状态为补证。 [质量规则第 9 节](../../../docs/operations/agent-code-quality-rules.md#L267)

RouteGroup 拼接前缀后继续传递 route options。空相对路径与正常子路径的匹配及 URL 同步值得单测。 [core/routing.go:154](../../../core/routing.go#L154)

[复审报告](../../../docs/evidence/stable-quality-review-999d5e4d.md)

## 验收与验证

- [x] 完整前缀一致且命名可查询，添加失败时不出现孤立的名称。
- [x] 记录挂载入口、具体断言和结果。已有测试能覆盖时复用并注明。

```sh
go test -race -timeout 60s ./core
```

## 实施证据

- [TestGroupEmptyRelativePathNamedRoute](../../../core/routing_test.go#L689)
