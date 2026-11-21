# Card 1748 — 关闭的缓存仍保留已不可读取的全部条目

Milestone: 稳定模块质量复审
Recipe: specs/change-recipes/fix-bug.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: store
Owned Files: `store/cache/memory.go`, `store/cache/state_regression_test.go`, `docs/modules/store/README.md`, `store/cache/stats.go`
Depends On: 无

Goal: 验证挂起操作、重复关闭、后续拒绝及真实 payload 引用释放。
Scope: 用受控在途读写核对关闭契约，评估屏障后解除对 payload 的持有，同时保留已承诺的终态统计。
Non-goals: 维持稳定公开签名与标准库依赖，将验证限于本卡范围。
Files: `store/cache/memory.go`, `store/cache/state_regression_test.go`, `docs/modules/store/README.md`, `store/cache/stats.go`。
Tests: 验证挂起操作、重复关闭、后续拒绝及真实 payload 引用释放。
Docs Sync: 复核模块说明并验证生成数据。
Done Definition: 完成下列验收，记录验证命令、状态及剩余限制。
Outcome: 关闭屏障后清空已不可读取的 payload。统计仍反映最后一次已接受写入，重复 Close 保留最终统计，与关闭交叠的缺失读取会复核缓存状态。

## 证据与影响

第 9 节检查号为 6,28,29,34,35。证据状态为源码确认。

Close 等待清理和写入完成，未丢弃条目。已关闭实例被应用继续持有时保留 payload。关闭后统计已被近期测试用来观察最终写入，清空需先决定兼容性。 [store/cache/memory.go:445](../../../store/cache/memory.go#L445)

[复审报告](../../../docs/evidence/stable-quality-review-d224acbc.md)

## 验收与验证

- [x] 验证挂起操作、重复关闭、后续拒绝及真实 payload 引用释放。
- [x] 记录运行结果。若已有用例覆盖目标，记录用例及相关断言。

```sh
go test -race -timeout 60s ./store/cache
make website-sync
make website-check-generated
```

## 实施证据

- [TestCloseTracksFinalPendingWriteInStats](../../../store/cache/state_regression_test.go#L213)
- [TestDeleteCanceledWhileCloseWaitsRetainsFinalCounts](../../../store/cache/state_regression_test.go#L395)

[批次验证与基准](../review-d224acbc/README.md)
