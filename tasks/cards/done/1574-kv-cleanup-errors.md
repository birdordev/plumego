# Card 1574 — KV 临时快照回滚丢弃清理错误

Milestone: 本轮质量审查
Recipe: specs/change-recipes/fix-bug.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: store
Owned Files: `store/kv/persistence.go`, `store/kv/state_regression_test.go`
Depends On: 无

Goal: KV 临时快照回滚丢弃清理错误。
Scope: 仅在提交前收集有意义的关闭和删除错误，与主因合并，忽略文件已经不存在。
Non-goals: 保留现有公开签名和依赖。涉及未授权的行为或兼容契约调整时，先完成调用方核对与兼容决策。
Files: `store/kv/persistence.go`, `store/kv/state_regression_test.go`。
Tests: 注入写主错误与清理错误，断言两者可分类、原快照仍可打开，且下一次写入可用。
Docs Sync: 本卡按现有契约验证。若取证表明契约需调整，先更新后续卡范围。
Done Definition: 完成下列验收和验证，记录证据与剩余风险。
Outcome: 临时快照写或同步失败时保留主错误、关闭错误和删除错误。原状态文件仍可读，复用公开入口重试回归。

## 证据与影响

- 检查号：22,29,30,41,46。证据状态：源码确认。
- 写入和同步失败时丢弃 Close 错误；提交前退出时也丢弃临时文件删除错误。调用者只能看到主故障，无法诊断可能残留的资源。 [store/kv/persistence.go:180](../../../store/kv/persistence.go#L180)
- [审查报告](../../../docs/evidence/stable-quality-review-dee7aa0f.md)

## 验收与验证

- [x] 注入写主错误与清理错误，断言两者可分类、原快照仍可打开，且下一次写入可用。
- [x] 运行以下命令并记录结果、失败路径和未验证范围。修改 Go 文件前完成仓库预检，验证前先执行仓库格式化。

```sh
go test -race -timeout 60s ./store/kv
go vet ./store/...
make boundary-check api-check
```

## 实施证据

[TestSnapshotWritePreservesCleanupErrors](../../../store/kv/state_regression_test.go#L438) · [TestWriteFailureRestoresEvictionAndAllowsRetry](../../../store/kv/state_regression_test.go#L127)
