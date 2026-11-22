# Card 2138 — 核对多个 Close 调用者等待同一写入完成

Milestone: 稳定模块第 9 节复审
Recipe: specs/change-recipes/analysis-only.yaml
Context Package: review
Priority: P2
State: done
Primary Module: store/cache
Owned Files: `store/cache/state_regression_test.go`
Depends On: 无

Goal: 核对多个 Close 调用者等待同一写入完成。
Scope: 通道固定进行中写入和两个 Close 的重叠，核对 closeOnce 等待与最终统计。
Non-goals: 本卡提交取证结果。后续行为变更由独立修复卡执行。
Files: `store/cache/state_regression_test.go`
Tests: 断言两个 Close 返回后存储已释放、后续操作返回 ErrCacheClosed，Stats 保留最终使用量。
Docs Sync: 核对既有文档与验收结果一致，记录需要后续同步的位置。
Done Definition: 记录命令、结果、证据限制和是否需要独立修复卡。
Outcome: 受控进行中写入和两个 Close 调用验证，数据在两个 Close 返回前已释放，最终统计保留最后写入量。首次探针使用 synctest 等待互斥锁导致探针超时，已改成通道同步并重跑通过。

## 证据与影响

证据状态为待补证。第 9 节检查号为 28,29,32,33,41,47。已有单个 Close 与进行中写入的测试。多个 Close 调用者是否在同一清理结果后一起返回，有待在同一时序复验。 [store/cache/memory.go:446](../../../store/cache/memory.go#L446) [store/cache/memory.go:472](../../../store/cache/memory.go#L472) [store/cache/state_regression_test.go:213](../../../store/cache/state_regression_test.go#L213)

[复审报告](../../../docs/evidence/stable-quality-review-6e8d53f9.md)

## 验收与验证

- [x] 断言两个 Close 返回后存储已释放、后续操作返回 ErrCacheClosed，Stats 保留最终使用量。
- [x] 记录命令、结果、证据限制和是否需要独立修复卡。

```sh
go test -race -timeout 60s ./store/cache
```
