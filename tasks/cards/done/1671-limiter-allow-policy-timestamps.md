# Card 1671 — 验证 limiter Reset 与 RetryAfter 的极值语义

Milestone: 稳定模块质量复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: security
Owned Files: `security/abuse/finite_regression_test.go`
Depends On: 无

Goal: 验证 limiter Reset 与 RetryAfter 的极值语义。
Scope: 以固定时钟比较正常极值、枯竭和恢复。
Non-goals: 维持公开签名和依赖；涉及现有兼容契约的调整须先核对调用边界及迁移影响。
Files: `security/abuse/finite_regression_test.go`。
Tests: 非负等待值，Reset 不倒流；拒绝时 Remaining 有明确含义。
Docs Sync: 对照原契约核验，若证据要求修订公开行为，先更新实施范围。
Done Definition: 完成测试与验收记录，并标明剩余取证范围。
Outcome: 固定时钟下正常、极小和极大速率的等待时间非负，恢复符合速率边界。

## 证据

检查号为 14,32,35,40，状态为补证。

速率极值已有有限数检查，多个时间字段应随允许结果保持一致。 [security/abuse/limiter.go:418](../../../security/abuse/limiter.go#L418)

[复审报告](../../../docs/evidence/stable-quality-review-09a7f5d3.md)

## 验收与验证

- [x] 非负等待值，Reset 不倒流；拒绝时 Remaining 有明确含义。
- [x] 记录验证命令、状态和剩余范围。若使用既有用例满足验收，记录对应证据。

```sh
go test -race -timeout 60s ./security/abuse
```

## 实施证据

[TestFiniteRateDecisionTimestampsStayOrdered](../../../security/abuse/finite_regression_test.go#L36)
