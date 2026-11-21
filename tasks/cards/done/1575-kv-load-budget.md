# Card 1575 — KV 配额在全量快照解码后才生效

Milestone: 本轮质量审查
Recipe: specs/change-recipes/fix-bug.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: store
Owned Files: `store/kv/persistence.go`, `store/kv/kv_test.go`, `docs/modules/store/README.md`, `website/src/generated/modules.ts`
Depends On: 无

Goal: KV 配额在全量快照解码后才生效。
Scope: 先定磁盘输入预算与历史超配额快照的兼容策略，再在分配前检查输入或用有界解析。
Non-goals: 保留现有公开签名和依赖。涉及未授权的行为或兼容契约调整时，先完成调用方核对与兼容决策。
Files: `store/kv/persistence.go`, `store/kv/kv_test.go`, `docs/modules/store/README.md`, `website/src/generated/modules.ts`。
Tests: 覆盖正常旧格式、临界文件和裁剪兼容，测量峰值内存，确认拒绝时目录锁可重新取得。
Docs Sync: 核对 `docs/modules/store/README.md`。 修改模块说明后检查网站生成差异，并在同一交付中保留相应生成文件。
Done Definition: 完成下列验收和验证，记录证据与剩余风险。
Outcome: 读取前检查快照大小，读取中也应用预算。兼容历史/小配额裁剪和目录锁释放检查通过。Linux 容器中 8000 条裁剪到 4000 条样本约 7.43 MB/op，采样到的进程峰值为 11244 KiB。

## 证据与影响

- 检查号：14,29,35,41,48。证据状态：源码确认。
- 启动先读取、解码并复扫整个文件，构造器随后才裁剪条目。当前内存配置限制驻留数据，无法限制打开过大历史快照时的瞬时内存。 [store/kv/persistence.go:25](../../../store/kv/persistence.go#L25)
- [审查报告](../../../docs/evidence/stable-quality-review-dee7aa0f.md)

## 验收与验证

- [x] 覆盖正常旧格式、临界文件和裁剪兼容，测量峰值内存，确认拒绝时目录锁可重新取得。
- [x] 运行以下命令并记录结果、失败路径和未验证范围。修改 Go 文件前完成仓库预检，验证前先执行仓库格式化。

```sh
go test -race -timeout 60s ./store/kv
make website-sync
make website-check-generated
```

## 实施证据

[TestSnapshotReadBudget](../../../store/kv/kv_test.go#L1848) · [TestPersistLockedPreservesLegacySnapshotEncoding](../../../store/kv/kv_test.go#L472) · [TestKVStoreStartupTrimsExpiredEntriesAndBothCapacityLimits](../../../store/kv/kv_test.go#L1411) · [TestDirectoryLockReleasedAfterLoadFailure](../../../store/kv/lock_test.go#L46)
