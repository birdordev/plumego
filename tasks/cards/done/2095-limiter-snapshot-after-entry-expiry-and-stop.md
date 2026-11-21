# Card 2095 — 限流过期清理与停止的统计顺序

Milestone: 稳定模块第 9 节复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: security/abuse
Owned Files: `security/abuse/limiter_test.go`
Depends On: 无

Goal: 限流过期清理与停止的统计顺序
Scope: 用注入钟推进到普通占位条目过期点，再调用 Stop 并检查一次后续请求。
Non-goals: 范围限于本卡涉及的契约，保持公开签名和依赖。
Files: `security/abuse/limiter_test.go`。
Tests: 记录清理只在启用时发生，决策计数不随旧键删除倒退。
Docs Sync: 核对新增验收与已有模块说明一致，记录证据和限制。
Done Definition: 完成以下验收，记录命令、结果与剩余限制。
Outcome: 在请求驱动清理回归中固定过期、自定义时钟 Stop 及统计，桶数量按预期变化而总决策继续累加。

## 证据与影响

证据状态为补证。第 9 节检查号为 28,32,35,40,47。 [质量规则第 9 节](../../../docs/operations/agent-code-quality-rules.md#L267)

请求驱动清理与 Stop 切换，需分别观察字典数量和允许/拒绝累加。 [security/abuse/limiter.go:470](../../../security/abuse/limiter.go#L470)

[复审报告](../../../docs/evidence/stable-quality-review-2f962d21.md)

## 验收与验证

- [x] 记录清理只在启用时发生，决策计数不随旧键删除倒退。
- [x] 已有测试覆盖相同触发和断言时可直接复用，记录具体用例。

```sh
go test -race -timeout 60s ./security/abuse
```

## 实施证据

- [TestAcceptanceCustomClockCleanupAndStop](../../../security/abuse/limiter_test.go#L870)
