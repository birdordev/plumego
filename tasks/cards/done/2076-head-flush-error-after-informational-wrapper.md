# Card 2076 — HEAD 信息响应后拒绝 Flush 的终态

Milestone: 稳定模块第 9 节复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: router
Owned Files: `router/head_regression_test.go`
Depends On: 无

Goal: HEAD 信息响应后拒绝 Flush 的终态
Scope: 经一层 Unwrap 派发 103，让底层 FlushError 返回 ErrNotSupported，随后设最终状态及 header。
Non-goals: 范围限于本卡涉及的契约，保持公开签名和依赖。
Files: `router/head_regression_test.go`。
Tests: 103 与最终状态各一次，最终头取改写后的合法值。
Docs Sync: 核对新增验收与已有模块说明一致，记录证据和限制。
Done Definition: 完成以下验收，记录命令、结果与剩余限制。
Outcome: 回归验证 103 后的 Flush 拒绝不会提交最终状态，后来写入的 202 与最终头保留。

## 证据与影响

证据状态为补证。第 9 节检查号为 23,24,37,46。 [质量规则第 9 节](../../../docs/operations/agent-code-quality-rules.md#L267)

仅信息响应还未选最终状态。能力明确拒绝时可保留后续响应机会。 [router/dispatch.go:116](../../../router/dispatch.go#L116)

[复审报告](../../../docs/evidence/stable-quality-review-2f962d21.md)

## 验收与验证

- [x] 103 与最终状态各一次，最终头取改写后的合法值。
- [x] 已有测试覆盖相同触发和断言时可直接复用，记录具体用例。

```sh
go test -race -timeout 60s ./router
```

## 实施证据

- [TestHeadInformationalThenDeclinedFlushKeepsFinalResponseOpen](../../../router/head_regression_test.go#L646)
