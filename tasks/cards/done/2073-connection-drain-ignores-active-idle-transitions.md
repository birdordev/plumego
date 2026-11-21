# Card 2073 — 排空诊断只计连接而非请求轮次

Milestone: 稳定模块第 9 节复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: core
Owned Files: `core/lifecycle_test.go`
Depends On: 无

Goal: 排空诊断只计连接而非请求轮次
Scope: 以回环 keepalive 执行两次请求，按 ConnState 通道读取状态与打开数。
Non-goals: 范围限于本卡涉及的契约，保持公开签名和依赖。
Files: `core/lifecycle_test.go`。
Tests: 复用同一连接时计数稳定，终态归零，后续 Shutdown 归还诊断所有权。
Docs Sync: 核对新增验收与已有模块说明一致，记录证据和限制。
Done Definition: 完成以下验收，记录命令、结果与剩余限制。
Outcome: 回环复用同一个连接跑两个请求。Active 和 Idle 保持计数为一，关闭后归零，排空所有权归还。

## 证据与影响

证据状态为补证。第 9 节检查号为 28,29,32,36,47。 [质量规则第 9 节](../../../docs/operations/agent-code-quality-rules.md#L267)

ConnState Active 和 Idle 多次重复，计量只在 New 和终态更新。 [core/lifecycle.go:122](../../../core/lifecycle.go#L122)

[复审报告](../../../docs/evidence/stable-quality-review-2f962d21.md)

## 验收与验证

- [x] 复用同一连接时计数稳定，终态归零，后续 Shutdown 归还诊断所有权。
- [x] 已有测试覆盖相同触发和断言时可直接复用，记录具体用例。

```sh
go test -race -timeout 60s ./core
```

## 实施证据

- [TestConnectionTrackerCountsKeepAliveConnectionAcrossRequests](../../../core/lifecycle_test.go#L1566)
