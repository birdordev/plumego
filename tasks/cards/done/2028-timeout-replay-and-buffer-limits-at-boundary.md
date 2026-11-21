# Card 2028 — Timeout 两种预算次序和精确边界

Milestone: 稳定模块第 9 节复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: middleware/timeout
Owned Files: `middleware/timeout/timeout_regression_test.go`
Depends On: 无

Goal: Timeout 两种预算次序和精确边界
Scope: 用两段写走精确上限、上限加一与两个预算同时越界三组。
Non-goals: 范围限于本卡涉及的契约，保持公开签名和依赖。
Files: `middleware/timeout/timeout_regression_test.go`。
Tests: 边界内正常回放，越界为规范错误，原表示头和 body 不漏出。
Docs Sync: 核对新增验收与已有模块说明一致，记录证据和限制。
Done Definition: 完成以下验收，记录命令、结果与剩余限制。
Outcome: 双上限精确命中时回放成功，各预算单独越界和同时越界均给出对应错误，无原表示头或正文。

## 证据与影响

证据状态为补证。第 9 节检查号为 14,23,24,35,39,46。 [质量规则第 9 节](../../../docs/operations/agent-code-quality-rules.md#L267)

Timeout 先判重放预算，再交给带缓冲预算的 writer。配置两者大小可交换。 [middleware/timeout/timeout.go:266](../../../middleware/timeout/timeout.go#L266)

[复审报告](../../../docs/evidence/stable-quality-review-c3a77410.md)

## 验收与验证

- [x] 边界内正常回放，越界为规范错误，原表示头和 body 不漏出。
- [x] 已有测试覆盖相同触发和断言时可直接复用，记录具体用例。

```sh
go test -race -timeout 60s ./middleware/timeout
```

## 实施证据

- [TestTimeoutBufferAndReplayLimitsAtExactBoundary](../../../middleware/timeout/timeout_regression_test.go#L542)
