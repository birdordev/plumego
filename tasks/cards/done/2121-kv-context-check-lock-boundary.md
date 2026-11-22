# Card 2121 — 移出 KV 锁内的调用方上下文方法

Milestone: 稳定模块第 9 节复审
Recipe: specs/change-recipes/fix-bug.yaml
Context Package: implementation
Priority: P1
State: done
Primary Module: store/kv
Owned Files: `store/kv/kv.go`, `store/kv/state_regression_test.go`
Depends On: 无

Goal: 移出 KV 锁内的调用方上下文方法。
Scope: 锁外取得取消信号，持锁时检查通道。需要错误原因时先恢复未提交状态并释放锁，再读取错误。保留重命名前后对取消和同步失败的既有规则。
Non-goals: 保持公开签名、依赖和其余模块的行为。
Files: `store/kv/kv.go`, `store/kv/state_regression_test.go`
Tests: 由测试上下文记录 Err 是否能读取同一存储，以通道控制锁和取消。验证取消后内存、磁盘和重开一致，以及已提交路径不回滚。
Docs Sync: 核对 KV 取消、重命名和目录同步的说明，行为边界发生变化时同步所属说明。
Done Definition: 完成以下回归和验收，记录实际命令及结果。
Outcome: KV 锁外取得 Done，锁内只观察信号。启用取消后先撤销未提交的暂存数据，再解锁并返回原始上下文错误；目录同步失败且替换已提交时保持新数据。取消上下文重入读方法与提交前后回归均通过。持久化锁仍串行，取消可在取得锁后被处理。

## 证据与影响

证据状态为源码确认。第 9 节检查号为 26,30,31,32,33,41,46。公开读写入口在持有读锁或写锁时调用调用方提供的 Context.Err。自定义上下文同步读取同一 store 时会与写锁互等，普通标准库上下文未显示此问题。 [store/kv/kv.go:113](../../../store/kv/kv.go#L113) [store/kv/kv.go:169](../../../store/kv/kv.go#L169) [store/kv/kv.go:355](../../../store/kv/kv.go#L355) [store/kv/kv.go:532](../../../store/kv/kv.go#L532)

[复审报告](../../../docs/evidence/stable-quality-review-6e8d53f9.md)

## 验收与验证

- [x] 由测试上下文记录 Err 是否能读取同一存储，以通道控制锁和取消。验证取消后内存、磁盘和重开一致，以及已提交路径不回滚。
- [x] 完成以下回归和验收，记录实际命令及结果。

```sh
go test -race -timeout 60s ./store/kv
```

实现时先运行 `make fmt`，按实际改动范围预览并运行 diff 门禁。
