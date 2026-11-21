# Card 2110 — 文本自定义格式失败后的界线

Milestone: 稳定模块第 9 节复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: log
Owned Files: `log/safety_regression_test.go`
Depends On: 无

Goal: 文本自定义格式失败后的界线
Scope: 使用普通 Formatter 写部分值后 panic，再记录下一条。
Non-goals: 范围限于本卡涉及的契约，保持公开签名和依赖。
Files: `log/safety_regression_test.go`。
Tests: 只出现受约束值和两个独立记录边界，继承字段保持。
Docs Sync: 核对新增验收与已有模块说明一致，记录证据和限制。
Done Definition: 完成以下验收，记录命令、结果与剩余限制。
Outcome: 复用已有 Formatter 写一半后 panic 的安全回归，并验证紧随的下一条记录和继承字段边界。

## 证据与影响

证据状态为补证。第 9 节检查号为 25,33,43,46。 [质量规则第 9 节](../../../docs/operations/agent-code-quality-rules.md#L267)

字段的自定义格式执行在封闭缓冲内。需验证合法前缀后抛错时半截字段不跨到下一条。 [log/logger.go:399](../../../log/logger.go#L399)

[复审报告](../../../docs/evidence/stable-quality-review-2f962d21.md)

## 验收与验证

- [x] 只出现受约束值和两个独立记录边界，继承字段保持。
- [x] 已有测试覆盖相同触发和断言时可直接复用，记录具体用例。

```sh
go test -race -timeout 60s ./log
```

## 实施证据

- [TestTextLoggerContainsFormatPanics](../../../log/safety_regression_test.go#L45)
