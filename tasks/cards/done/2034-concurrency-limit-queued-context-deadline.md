# Card 2034 — 排队期限与上游提前结束

Milestone: 稳定模块第 9 节复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: middleware/concurrencylimit
Owned Files: `middleware/concurrencylimit/cancel_race_test.go`
Depends On: 无

Goal: 排队期限与上游提前结束
Scope: 占满工作槽后把有上游期限的第二请求送入队列，确认退出再释放首请求。
Non-goals: 范围限于本卡涉及的契约，保持公开签名和依赖。
Files: `middleware/concurrencylimit/cancel_race_test.go`。
Tests: 被取消的处理器调用数为零，后继普通请求仍可接纳。
Docs Sync: 核对新增验收与已有模块说明一致，记录证据和限制。
Done Definition: 完成以下验收，记录命令、结果与剩余限制。
Outcome: 确定性时钟下，上游期限先于队列超时到期，请求不进入下游，释放后正常请求可接纳。

## 证据与影响

证据状态为补证。第 9 节检查号为 20,26,28,35,39,47。 [质量规则第 9 节](../../../docs/operations/agent-code-quality-rules.md#L267)

排队可由请求 context 或内部队列时间结束。需要以同步信号核验先到的请求期限。 [middleware/concurrencylimit/concurrency_limit.go:105](../../../middleware/concurrencylimit/concurrency_limit.go#L105)

[复审报告](../../../docs/evidence/stable-quality-review-c3a77410.md)

## 验收与验证

- [x] 被取消的处理器调用数为零，后继普通请求仍可接纳。
- [x] 已有测试覆盖相同触发和断言时可直接复用，记录具体用例。

```sh
go test -race -timeout 60s ./middleware/concurrencylimit
```

## 实施证据

- [TestQueuedRequestDeadlineExpiresBeforeQueueTimeout](../../../middleware/concurrencylimit/cancel_race_test.go#L131)
