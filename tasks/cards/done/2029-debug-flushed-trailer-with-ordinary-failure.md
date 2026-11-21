# Card 2029 — Debug 已透传时的写失败及尾字段

Milestone: 稳定模块第 9 节复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: middleware/debug
Owned Files: `middleware/debug/debug_errors_test.go`
Depends On: 无

Goal: Debug 已透传时的写失败及尾字段
Scope: 以可控 writer 返回一次错误，用已声明的简单尾字段检查一次收尾。
Non-goals: 范围限于本卡涉及的契约，保持公开签名和依赖。
Files: `middleware/debug/debug_errors_test.go`。
Tests: 保留原状态和写错误，无额外最终状态，尾字段按已提交路径传递。
Docs Sync: 核对新增验收与已有模块说明一致，记录证据和限制。
Done Definition: 完成以下验收，记录命令、结果与剩余限制。
Outcome: 已透传后的普通部分写入返回底层错误，提交一次并收尾转交已声明尾字段。

## 证据与影响

证据状态为补证。第 9 节检查号为 23,24,39,46。 [质量规则第 9 节](../../../docs/operations/agent-code-quality-rules.md#L267)

已验证回环 panic 路径，普通底层写失败后正常返回的透传收尾没有同类证据。 [middleware/debug/debug_errors.go:291](../../../middleware/debug/debug_errors.go#L291)

[复审报告](../../../docs/evidence/stable-quality-review-c3a77410.md)

## 验收与验证

- [x] 保留原状态和写错误，无额外最终状态，尾字段按已提交路径传递。
- [x] 已有测试覆盖相同触发和断言时可直接复用，记录具体用例。

```sh
go test -race -timeout 60s ./middleware/debug
```

## 实施证据

- [TestDebugCommittedTrailerSurvivesOrdinaryWriteFailure](../../../middleware/debug/debug_errors_test.go#L1270)
