# Card 2020 — 显式零长度 HEAD 保持声明

Milestone: 稳定模块第 9 节复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: router
Owned Files: `router/head_regression_test.go`
Depends On: 2008

Goal: 显式零长度 HEAD 保持声明
Scope: 用真实 HEAD 请求读取显式 Content-Length: 0 与无写入结果。
Non-goals: 范围限于本卡涉及的契约，保持公开签名和依赖。
Files: `router/head_regression_test.go`。
Tests: 显式声明保留且 body 为空。依赖 2008 的实现契约。
Docs Sync: 核对新增验收与已有模块说明一致，记录证据和限制。
Done Definition: 完成以下验收，记录命令、结果与剩余限制。
Outcome: 回环 HTTP 显式零长度保留，202 和空正文按原样传输。

## 证据与影响

证据状态为补证。第 9 节检查号为 6,24,37,45。 [质量规则第 9 节](../../../docs/operations/agent-code-quality-rules.md#L267)

自动长度推断须区分显式零和缺失声明，已有空头缺口需要保护合法零。 [router/dispatch.go:90](../../../router/dispatch.go#L90)

[复审报告](../../../docs/evidence/stable-quality-review-c3a77410.md)

## 验收与验证

- [x] 显式声明保留且 body 为空。依赖 2008 的实现契约。
- [x] 已有测试覆盖相同触发和断言时可直接复用，记录具体用例。

```sh
go test -race -timeout 60s ./router
```

## 实施证据

- [TestHeadHandlerKeepsExplicitZeroLength](../../../router/head_regression_test.go#L621)
