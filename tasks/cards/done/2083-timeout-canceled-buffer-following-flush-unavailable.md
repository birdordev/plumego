# Card 2083 — 超时后迟到 Flush 和正文隔离

Milestone: 稳定模块第 9 节复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: middleware/timeout
Owned Files: `middleware/timeout/timeout_regression_test.go`
Depends On: 无

Goal: 超时后迟到 Flush 和正文隔离
Scope: 用通道固定响应返回后才尝试 Flush 与 Write，底层记录任何额外提交。
Non-goals: 范围限于本卡涉及的契约，保持公开签名和依赖。
Files: `middleware/timeout/timeout_regression_test.go`。
Tests: 504 保持，迟到写无接收字节，不产生额外状态或外层 flush。
Docs Sync: 核对新增验收与已有模块说明一致，记录证据和限制。
Done Definition: 完成以下验收，记录命令、结果与剩余限制。
Outcome: 确定性超时后下游 Flush 返回不支持，Write 返回 DeadlineExceeded，外层保留一次 504。

## 证据与影响

证据状态为补证。第 9 节检查号为 23,24,26,39,47。 [质量规则第 9 节](../../../docs/operations/agent-code-quality-rules.md#L267)

缓冲 writer 在超时后会拒绝正文。能力不可用的 Flush 与迟到正文组合需验证。 [middleware/timeout/timeout.go:241](../../../middleware/timeout/timeout.go#L241)

[复审报告](../../../docs/evidence/stable-quality-review-2f962d21.md)

## 验收与验证

- [x] 504 保持，迟到写无接收字节，不产生额外状态或外层 flush。
- [x] 已有测试覆盖相同触发和断言时可直接复用，记录具体用例。

```sh
go test -race -timeout 60s ./middleware/timeout
```

## 实施证据

- [TestTimedOutHandlerCannotFlushOrWriteToOuterResponse](../../../middleware/timeout/timeout_regression_test.go#L576)
