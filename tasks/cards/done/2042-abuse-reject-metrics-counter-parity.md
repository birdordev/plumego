# Card 2042 — 限流拒绝与进程内计数一致

Milestone: 稳定模块第 9 节复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: security/abuse
Owned Files: `security/abuse/limiter_test.go`
Depends On: 无

Goal: 限流拒绝与进程内计数一致
Scope: 固定时钟避免令牌回充，对同一 key 发出确定数量的请求。
Non-goals: 范围限于本卡涉及的契约，保持公开签名和依赖。
Files: `security/abuse/limiter_test.go`。
Tests: 允许与拒绝计数和返回决策逐项对应，停止后计数按既有语义记录。
Docs Sync: 核对新增验收与已有模块说明一致，记录证据和限制。
Done Definition: 完成以下验收，记录命令、结果与剩余限制。
Outcome: 固定时钟下前两次允许，随后两次拒绝，Stop 后仍按决策增长拒绝计数。

## 证据与影响

证据状态为补证。第 9 节检查号为 28,32,40,44。 [质量规则第 9 节](../../../docs/operations/agent-code-quality-rules.md#L267)

Limiter 同时维护请求结果和统计。用同一个普通测试 key 的固定小预算核验计数。 [security/abuse/limiter.go:574](../../../security/abuse/limiter.go#L574)

[复审报告](../../../docs/evidence/stable-quality-review-c3a77410.md)

## 验收与验证

- [x] 允许与拒绝计数和返回决策逐项对应，停止后计数按既有语义记录。
- [x] 已有测试覆盖相同触发和断言时可直接复用，记录具体用例。

```sh
go test -race -timeout 60s ./security/abuse
```

## 实施证据

- [TestLimiterCountsFixedAdmitAndRejectDecisionsThroughStop](../../../security/abuse/limiter_test.go#L1052)
