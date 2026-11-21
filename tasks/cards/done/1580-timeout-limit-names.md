# Card 1580 — 超时 writer 的旁路命名与实际行为不符

Milestone: 本轮质量审查
Recipe: specs/change-recipes/fix-bug.yaml
Context Package: implementation
Priority: P3
State: done
Primary Module: middleware
Owned Files: `middleware/timeout/timeout.go`, `middleware/timeout/timeout_test.go`
Depends On: 无

Goal: 超时 writer 的旁路命名与实际行为不符。
Scope: 重命名私有字段并让注释描述回放拒绝。
Non-goals: 保留现有公开签名和依赖。涉及未授权的行为或兼容契约调整时，先完成调用方核对与兼容决策。
Files: `middleware/timeout/timeout.go`, `middleware/timeout/timeout_test.go`。
Tests: 复用现有回放容量测试，保持两个预算触发的状态、响应体和调用次数。
Docs Sync: 本卡按现有契约验证。若取证表明契约需调整，先更新后续卡范围。
Done Definition: 完成下列验收和验证，记录证据与剩余风险。
Outcome: 重命名私有字段为 replayLimitExceeded，保留两个缓冲限制各自的现有传输行为。

## 证据与影响

- 检查号：9,10,16,23。证据状态：源码确认。
- 超过回放预算时丢弃后续写入并返回结构化 500，bypassUsed 容易被理解为透传。 [middleware/timeout/timeout.go:219](../../../middleware/timeout/timeout.go#L219)
- [审查报告](../../../docs/evidence/stable-quality-review-dee7aa0f.md)

## 验收与验证

- [x] 复用现有回放容量测试，保持两个预算触发的状态、响应体和调用次数。
- [x] 运行以下命令并记录结果、失败路径和未验证范围。修改 Go 文件前完成仓库预检，验证前先执行仓库格式化。

```sh
go test -race -timeout 60s ./middleware/timeout
```

## 实施证据

[TestTimeoutMiddleware_LargeResponseExceedsMaxReplayBytes](../../../middleware/timeout/timeout_test.go#L472) · [TestTimeoutMiddleware_MaxReplayBytes](../../../middleware/timeout/timeout_test.go#L552)
