# Card 2072 — App.Any 的命名与标签贯通

Milestone: 稳定模块第 9 节复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P3
State: done
Primary Module: core
Owned Files: `core/routing_test.go`
Depends On: 无

Goal: App.Any 的命名与标签贯通
Scope: 用小型分派从非 GET 请求、Routes 和 URL 读取同一名称与标签。
Non-goals: 范围限于本卡涉及的契约，保持公开签名和依赖。
Files: `core/routing_test.go`。
Tests: 反查生成正确路径，公开快照可写且不会污染后续读取。
Docs Sync: 核对新增验收与已有模块说明一致，记录证据和限制。
Done Definition: 完成以下验收，记录命令、结果与剩余限制。
Outcome: 新增 App.Any 的 DELETE 分派、命名反查和标签隔离断言，三条路径符合公开契约。

## 证据与影响

证据状态为补证。第 9 节检查号为 6,19,34,36。 [质量规则第 9 节](../../../docs/operations/agent-code-quality-rules.md#L267)

Any 提供 opts 转交路径，需确认其路由快照与命名反查同普通方法保持一致。 [core/routing.go:79](../../../core/routing.go#L79)

[复审报告](../../../docs/evidence/stable-quality-review-2f962d21.md)

## 验收与验证

- [x] 反查生成正确路径，公开快照可写且不会污染后续读取。
- [x] 已有测试覆盖相同触发和断言时可直接复用，记录具体用例。

```sh
go test -race -timeout 60s ./core
```

## 实施证据

- [TestAppAnyRoundTripsNamedMetadata](../../../core/routing_test.go#L740)
