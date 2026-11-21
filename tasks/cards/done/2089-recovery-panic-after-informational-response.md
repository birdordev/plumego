# Card 2089 — 恢复层区分信息状态与最终错误

Milestone: 稳定模块第 9 节复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: middleware/recovery
Owned Files: `middleware/recovery/recovery_regression_test.go`
Depends On: 无

Goal: 恢复层区分信息状态与最终错误
Scope: 通过客户端信息响应钩子观察 103，再检查 recovery 的固定错误状态和一次日志。
Non-goals: 范围限于本卡涉及的契约，保持公开签名和依赖。
Files: `middleware/recovery/recovery_regression_test.go`。
Tests: 最终 500 一次，信息头和应用 panic 原值不进入错误 JSON。
Docs Sync: 核对新增验收与已有模块说明一致，记录证据和限制。
Done Definition: 完成以下验收，记录命令、结果与剩余限制。
Outcome: 回环钩子观察一次 103，恢复路径仅发送一次带固定错误码的 500，客户端正文不含 panic 原值，记录一条日志。

## 证据与影响

证据状态为补证。第 9 节检查号为 21,23,24,39,46。 [质量规则第 9 节](../../../docs/operations/agent-code-quality-rules.md#L267)

已写 103 时仍允许最终错误。需将网络头、错误信封和日志调用放在同一验证。 [middleware/recovery/recover.go:56](../../../middleware/recovery/recover.go#L56)

[复审报告](../../../docs/evidence/stable-quality-review-2f962d21.md)

## 验收与验证

- [x] 最终 500 一次，信息头和应用 panic 原值不进入错误 JSON。
- [x] 已有测试覆盖相同触发和断言时可直接复用，记录具体用例。

```sh
go test -race -timeout 60s ./middleware/recovery
```

## 实施证据

- [TestRecoveryAfterEarlyHintsWritesOneRedactedFinalResponse](../../../middleware/recovery/recovery_regression_test.go#L76)
