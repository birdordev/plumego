# Card 2023 — Print 失败 writer 的调用边界

Milestone: 稳定模块第 9 节复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P3
State: done
Primary Module: router
Owned Files: `router/router_print_test.go`
Depends On: 无

Goal: Print 失败 writer 的调用边界
Scope: 注入总是返回短写或错误的 writer，随后继续查路由。
Non-goals: 范围限于本卡涉及的契约，保持公开签名和依赖。
Files: `router/router_print_test.go`。
Tests: Print 按既有签名返回，处理器、快照和路由冻结状态保持可用。
Docs Sync: 核对新增验收与已有模块说明一致，记录证据和限制。
Done Definition: 完成以下验收，记录命令、结果与剩余限制。
Outcome: 复用失败 writer 回归。Print 停止当前输出，重试读取包含新注册路由的快照并可继续分派。

## 证据与影响

证据状态为补证。第 9 节检查号为 16,22,29,37。 [质量规则第 9 节](../../../docs/operations/agent-code-quality-rules.md#L267)

Print 公开签名没有错误返回，需锁定失败 writer 影响仅限诊断输出。 [router/metadata.go:203](../../../router/metadata.go#L203)

[复审报告](../../../docs/evidence/stable-quality-review-c3a77410.md)

## 验收与验证

- [x] Print 按既有签名返回，处理器、快照和路由冻结状态保持可用。
- [x] 已有测试覆盖相同触发和断言时可直接复用，记录具体用例。

```sh
go test -race -timeout 60s ./router
```

## 实施证据

- [TestPrintRetriesWithNewSnapshotAfterRegistrationAndFailure](../../../router/router_print_test.go#L91)
- [TestPrintOrdersSamePatternByMethodAcrossFreeze](../../../router/router_print_test.go#L118)
