# Card 1955 — Debug 在外层恢复 panic 后丢失已提交响应的 trailer

Milestone: 稳定模块质量复审
Recipe: specs/change-recipes/fix-bug.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: middleware/debug
Owned Files: `middleware/debug/debug_errors.go`, `middleware/debug/debug_errors_test.go`, `middleware/debug/README.md`
Depends On: 无

Goal: 用真实 HTTP 客户端读取 202、响应体和 trailer。覆盖恢复正常、未提交异常、Hijack 与普通透传，确保没有第二次提交。
Scope: 在调用下游前安排清理，只在已提交透传且未 Hijack 时复制现有尾字段。正常缓冲结果沿用现有提交路径，未提交异常不写响应。
Non-goals: 维持稳定公开签名与标准库依赖，将验证限于本卡范围。
Files: `middleware/debug/debug_errors.go`, `middleware/debug/debug_errors_test.go`, `middleware/debug/README.md`。
Tests: 用真实 HTTP 客户端读取 202、响应体和 trailer。覆盖恢复正常、未提交异常、Hijack 与普通透传，确保没有第二次提交。
Docs Sync: 已公开行为变更时同步包说明。
Done Definition: 完成下列验收，记录验证命令、状态及剩余限制。
Outcome: 用真实 HTTP 验证外层恢复后的 202、响应正文与尾字段。缓冲异常仍交由恢复层提交 500，接管连接继续由应用处理。

## 证据与影响

第 9 节检查号为 6,17,23,24,39,46，当前证据状态为已复现。 [质量规则第 9 节](../../../docs/operations/agent-code-quality-rules.md#L267)

本机 HTTP 服务中，外层 recovery 正常结束已提交的 202，响应体完整。加 Debug 后，panic 前写入的已声明尾字段消失；正常 Debug 和无 Debug 的恢复对照均保留尾字段。 [middleware/debug/debug_errors.go:132](../../../middleware/debug/debug_errors.go#L132)

[复审报告](../../../docs/evidence/stable-quality-review-999d5e4d.md)

## 验收与验证

- [x] 用真实 HTTP 客户端读取 202、响应体和 trailer。覆盖恢复正常、未提交异常、Hijack 与普通透传，确保没有第二次提交。
- [x] 记录挂载入口、具体断言和结果。已有测试能覆盖时复用并注明。

```sh
go test -race -timeout 60s ./middleware/debug
```

## 实施证据

- [TestDebugRecoveredStreamPreservesTrailers](../../../middleware/debug/debug_errors_test.go#L1224)
- [TestDebugSnapshotsFinalHeadersAndForwardsTrailers](../../../middleware/debug/debug_errors_test.go#L164)
- [TestDebugErrorsHijackWithStatusCommitsPassthrough](../../../middleware/debug/debug_errors_test.go#L946)

所属回归在 999d5e4d 的生产代码 overlay 上如预期失败，修改后连续十次竞态运行通过。
