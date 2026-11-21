# Card 2071 — 装配回调 panic 后重试取得所有权

Milestone: 稳定模块第 9 节复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: core
Owned Files: `core/core_public_test.go`
Depends On: 无

Goal: 装配回调 panic 后重试取得所有权
Scope: 中间件 builder 第一次抛固定值，外层恢复后第二次 Prepare，检查状态与路由。
Non-goals: 范围限于本卡涉及的契约，保持公开签名和依赖。
Files: `core/core_public_test.go`。
Tests: 原 panic 可观察，server 首次未发布，第二次请求按已注册链运行一次。
Docs Sync: 核对新增验收与已有模块说明一致，记录证据和限制。
Done Definition: 完成以下验收，记录命令、结果与剩余限制。
Outcome: 复用既有回归。第一次 builder panic 不发布 server，重试只发布一条有效中间件链。

## 证据与影响

证据状态为补证。第 9 节检查号为 23,28,33,36,47。 [质量规则第 9 节](../../../docs/operations/agent-code-quality-rules.md#L267)

恢复 handlerBuilding 的 defer 同时服务普通错误和额外退出方式。已有 Goexit 与 nil 结果证据，核对首次 panic 再成功的入口。 [core/http_handler.go:53](../../../core/http_handler.go#L53)

[复审报告](../../../docs/evidence/stable-quality-review-2f962d21.md)

## 验收与验证

- [x] 原 panic 可观察，server 首次未发布，第二次请求按已注册链运行一次。
- [x] 已有测试覆盖相同触发和断言时可直接复用，记录具体用例。

```sh
go test -race -timeout 60s ./core
```

## 实施证据

- [TestPublicPreparationRetriesMiddlewarePanic](../../../core/core_public_test.go#L386)
