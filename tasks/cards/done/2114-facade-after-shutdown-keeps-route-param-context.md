# Card 2114 — 门面应用关闭后的直接路径参数

Milestone: 稳定模块第 9 节复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P3
State: done
Primary Module: .
Owned Files: `plumego_test.go`
Depends On: 无

Goal: 门面应用关闭后的直接路径参数
Scope: 从门面装配命名参数路由，Prepare、Shutdown，再直接 ServeHTTP。
Non-goals: 范围限于本卡涉及的契约，保持公开签名和依赖。
Files: `plumego_test.go`。
Tests: 公开 Param 返回请求值，已关闭 server 语义按 core 保留。
Docs Sync: 核对新增验收与已有模块说明一致，记录证据和限制。
Done Definition: 完成以下验收，记录命令、结果与剩余限制。
Outcome: 门面装配参数路由后 Prepare、Shutdown，直接分派仍能从公开 Param 读取路由值。

## 证据与影响

证据状态为补证。第 9 节检查号为 3,6,27,36。 [质量规则第 9 节](../../../docs/operations/agent-code-quality-rules.md#L267)

门面保留路由参数便捷访问。核心已确认关闭后直接分派可用，将其和门面合并验证。 [plumego.go:73](../../../plumego.go#L73)

[复审报告](../../../docs/evidence/stable-quality-review-2f962d21.md)

## 验收与验证

- [x] 公开 Param 返回请求值，已关闭 server 语义按 core 保留。
- [x] 已有测试覆盖相同触发和断言时可直接复用，记录具体用例。

```sh
go test -race -timeout 60s .
```

## 实施证据

- [TestFacadeParamWorksAfterShutdownForDirectDispatch](../../../plumego_test.go#L287)
