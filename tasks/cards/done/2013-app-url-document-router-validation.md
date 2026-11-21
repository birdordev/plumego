# Card 2013 — 应用 URL 入口补齐参数约束说明

Milestone: 稳定模块第 9 节复审
Recipe: specs/change-recipes/fix-bug.yaml
Context Package: implementation
Priority: P3
State: done
Primary Module: core
Owned Files: `core/routing.go`, `core/routing_test.go`
Depends On: 无

Goal: 应用 URL 入口补齐参数约束说明
Scope: 引用 Router.URL 的完整校验契约，保留 App 特有未配置错误的说明。
Non-goals: 范围限于本卡涉及的契约，保持公开签名和依赖。
Files: `core/routing.go`, `core/routing_test.go`。
Tests: 从 App.URL 核验单段值含斜线和未知键时返回空 URL 和错误。
Docs Sync: 本卡同步所属公开入口或字段 GoDoc。
Done Definition: 完成以下验收，记录命令、结果与剩余限制。
Outcome: 已说明。App.URL 链接 Router.URL 的参数契约，新增斜线和未知键的应用入口断言。

## 证据与影响

证据状态为源码确认。第 9 节检查号为 6,16,37,52。 [质量规则第 9 节](../../../docs/operations/agent-code-quality-rules.md#L267)

App.URL 转发全部参数给 Router.URL。现有注释只列不存在、缺参和空参数，遗漏键必须成对及斜线约束。 [core/routing.go:42](../../../core/routing.go#L42)

[复审报告](../../../docs/evidence/stable-quality-review-c3a77410.md)

## 验收与验证

- [x] 从 App.URL 核验单段值含斜线和未知键时返回空 URL 和错误。
- [x] 已有测试覆盖相同触发和断言时可直接复用，记录具体用例。

```sh
go test -race -timeout 60s ./core
```

## 实施证据

- [TestAddRouteWithRouteNameRegistersURLAndReturnsErrors](../../../core/routing_test.go#L387)
