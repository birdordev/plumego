# Card 1615 — 补证跨进程目录锁的平台差异

Milestone: 本轮质量审查
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P2
State: blocked
Primary Module: store
Owned Files: `store/kv/kv_test.go`
Depends On: 原生 Windows 与不支持目录锁的目标系统运行环境

Goal: 补证跨进程目录锁的平台差异。
Scope: 复用现有子进程互斥用例并在 Linux 与 Windows 执行。
Non-goals: 保留现有公开签名、依赖与模块边界。测试若证实新的缺陷，先记录证据再单独安排修复。
Files: `store/kv/kv_test.go`。
Tests: 同目录并发打开受控失败，退出后可重新取得；无支持实现返回已定义错误。
Docs Sync: 本卡按现有契约验证。若取证表明契约需调整，先更新后续卡范围。
Done Definition: 完成下列验收和验证，记录证据与剩余风险。
Outcome: macOS 和 Linux arm64 执行目录锁、跨进程互斥、进程退出和加载失败后释放测试。Windows amd64 与 Plan 9 amd64 测试编译通过。仍需原生 Windows 运行互斥测试，并在不支持目录锁的目标系统执行拒绝路径。

## 证据与影响

- 检查号：28,29,41,46,50。证据状态：补证。
- 本机仅运行 darwin，三种平台锁实现仍需目标系统证据。 [store/kv/kv.go:89](../../../store/kv/kv.go#L89)
- [审查报告](../../../docs/evidence/stable-quality-review-dee7aa0f.md)

## 验收与验证

- [ ] 同目录并发打开受控失败，退出后可重新取得；无支持实现返回已定义错误。
- [ ] 运行以下命令并记录结果、失败路径和未验证范围。修改 Go 文件前完成仓库预检，验证前先执行仓库格式化。

```sh
go test -race -timeout 60s ./store/kv
```

## 实施证据

[TestDirectoryLock](../../../store/kv/lock_test.go#L12) · [TestDirectoryLockReleasedAfterLoadFailure](../../../store/kv/lock_test.go#L46)

已执行 `GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go test -c ./store/kv` 和相同配置的 Plan 9 目标编译。Linux arm64 容器内运行测试二进制，macOS arm64 运行 `go test -race ./store/kv -run ^TestDirectoryLock`。
