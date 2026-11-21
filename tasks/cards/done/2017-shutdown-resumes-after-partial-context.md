# Card 2017 — 第一次 Shutdown 取消后再次排空

Milestone: 稳定模块第 9 节复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: core
Owned Files: `core/lifecycle_test.go`
Depends On: 无

Goal: 第一次 Shutdown 取消后再次排空
Scope: 回环 listener 保持一个已开始请求，通过通道控制首次取消及第二次完成。
Non-goals: 范围限于本卡涉及的契约，保持公开签名和依赖。
Files: `core/lifecycle_test.go`。
Tests: 首个错误保留 context 分类，下一轮能成功排空且追踪对象释放。
Docs Sync: 核对新增验收与已有模块说明一致，记录证据和限制。
Done Definition: 完成以下验收，记录命令、结果与剩余限制。
Outcome: 复用回环并发回归。首次 Shutdown 取消保留 context.Canceled，后续完成排空并清除追踪对象。

## 证据与影响

证据状态为补证。第 9 节检查号为 26,28,29,36,47。 [质量规则第 9 节](../../../docs/operations/agent-code-quality-rules.md#L267)

排空日志拥有独立取消对象，重复 Shutdown 重新选择所有者。 [core/lifecycle.go:152](../../../core/lifecycle.go#L152)

[复审报告](../../../docs/evidence/stable-quality-review-c3a77410.md)

## 验收与验证

- [x] 首个错误保留 context 分类，下一轮能成功排空且追踪对象释放。
- [x] 已有测试覆盖相同触发和断言时可直接复用，记录具体用例。

```sh
go test -race -timeout 60s ./core
```

## 实施证据

- [TestConcurrentShutdownLeavesDrainVacantUntilNewCaller](../../../core/lifecycle_test.go#L1427)
