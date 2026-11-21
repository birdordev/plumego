# Card 1603 — 确定性覆盖并发队列退出与槽位回收

Milestone: 本轮质量审查
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: middleware
Owned Files: `middleware/concurrencylimit/cancel_race_test.go`
Depends On: 无

Goal: 确定性覆盖并发队列退出与槽位回收。
Scope: 先同步排队和释放，再推进合成计时。
Non-goals: 保留现有公开签名、依赖与模块边界。测试若证实新的缺陷，先记录证据再单独安排修复。
Files: `middleware/concurrencylimit/cancel_race_test.go`。
Tests: 覆盖取消先于释放、同时到期、释放先于期限，并验证下一请求可占用槽位。
Docs Sync: 本卡按现有契约验证。若取证表明契约需调整，先更新后续卡范围。
Done Definition: 完成下列验收和验证，记录证据与剩余风险。
Outcome: 复用合成时钟竞争、取消后槽位复用和成功排队的现有用例。

## 证据与影响

- 检查号：26,29,32,39,47。证据状态：补证。
- 已有竞争用例等待实际小延迟；可用合成时间和队列状态验证关键顺序。 [middleware/concurrencylimit/concurrency_limit.go:105](../../../middleware/concurrencylimit/concurrency_limit.go#L105)
- [审查报告](../../../docs/evidence/stable-quality-review-dee7aa0f.md)

## 验收与验证

- [x] 覆盖取消先于释放、同时到期、释放先于期限，并验证下一请求可占用槽位。
- [x] 运行以下命令并记录结果、失败路径和未验证范围。修改 Go 文件前完成仓库预检，验证前先执行仓库格式化。

```sh
go test -race -timeout 60s ./middleware/concurrencylimit
```

## 实施证据

[TestQueueTimeoutWinsSlotAdmission](../../../middleware/concurrencylimit/cancel_race_test.go#L54) · [TestCancellationWinsSlotAdmission](../../../middleware/concurrencylimit/cancel_race_test.go#L31) · [TestMiddlewareAllowsConfiguredQueuedWaiter](../../../middleware/concurrencylimit/concurrency_limit_test.go#L37)
