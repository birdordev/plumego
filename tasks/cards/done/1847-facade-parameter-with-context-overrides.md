# Card 1847 — 核对门面 Param 对请求上下文的透传顺序

Milestone: 稳定模块质量复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P3
State: done
Primary Module: .
Owned Files: `plumego_test.go`
Depends On: 无

Goal: 路由参数优先，未注册的键返回现有缺省结果。
Scope: 用同一公开 handler 对照同名父参数与实际路由参数。
Non-goals: 维持稳定公开签名与标准库依赖，将验证限于本卡范围。
Files: `plumego_test.go`。
Tests: 路由参数优先，未注册的键返回现有缺省结果。
Docs Sync: 复核本卡涉及的已公开约定。
Done Definition: 完成下列验收，记录验证命令、状态及剩余限制。
Outcome: 根门面沿用路由参数优先顺序，未注册键返回空字符串。

## 证据与影响

第 9 节检查号为 3,6,27,37。证据状态为补证。

门面直接调用 router.Param。旧用例仅覆盖单项命中和缺失。 [plumego.go:73](../../../plumego.go#L73)

[复审报告](../../../docs/evidence/stable-quality-review-a074eadb.md)

## 验收与验证

- [x] 路由参数优先，未注册的键返回现有缺省结果。
- [x] 记录运行结果。若已有用例覆盖目标，记录用例及相关断言。

```sh
go test -race -timeout 60s .
```

## 实施证据

- [TestParamFromRootFacade](../../../plumego_test.go#L161)

[批次验证与基准](../review-a074eadb/README.md)
