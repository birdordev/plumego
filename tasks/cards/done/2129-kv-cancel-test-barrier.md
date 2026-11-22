# Card 2129 — 消除 KV 锁等待取消测试的固定延迟

Milestone: 稳定模块第 9 节复审
Recipe: specs/change-recipes/fix-bug.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: store/kv
Owned Files: `store/kv/kv_test.go`
Depends On: 无

Goal: 消除 KV 锁等待取消测试的固定延迟。
Scope: 用明确的准入信号或 testing/synctest 等待 goroutine 阻塞，再取消并释放锁。
Non-goals: 保持公开签名、依赖和其余模块的行为。
Files: `store/kv/kv_test.go`
Tests: 重复运行，断言取消返回的分类、无新键和重开无写入。
Docs Sync: 本卡影响限于回归用例和注释。
Done Definition: 完成以下回归和验收，记录实际命令及结果。
Outcome: 用上下文 Done 取样信号替换 20 毫秒睡眠。取消后测试 Err 重新进入公开存储读路径，验证锁已释放、原始错误可分类且没有新键。

## 证据与影响

证据状态为源码确认。第 9 节检查号为 26,45,46,47。测试靠 20 毫秒延迟让 goroutine 进入等待，调度慢时也可能在入口发现预取消而通过，无法证明实际覆盖了持锁等待。 [store/kv/kv_test.go:905](../../../store/kv/kv_test.go#L905) [store/kv/kv_test.go:919](../../../store/kv/kv_test.go#L919)

[复审报告](../../../docs/evidence/stable-quality-review-6e8d53f9.md)

## 验收与验证

- [x] 重复运行，断言取消返回的分类、无新键和重开无写入。
- [x] 完成以下回归和验收，记录实际命令及结果。

```sh
go test -race -timeout 60s ./store/kv
```

实现时先运行 `make fmt`，按实际改动范围预览并运行 diff 门禁。
