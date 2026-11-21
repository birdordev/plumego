# Card 1613 — 补证内存缓存关停后的统计语义

Milestone: 本轮质量审查
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: store
Owned Files: `store/cache/state_regression_test.go`
Depends On: 无

Goal: 补证内存缓存关停后的统计语义。
Scope: 分别在写入提交前、Close 等待和结束后取得快照。
Non-goals: 保留现有公开签名、依赖与模块边界。测试若证实新的缺陷，先记录证据再单独安排修复。
Files: `store/cache/state_regression_test.go`。
Tests: 最终 Close 后统计稳定，校验写入条目与估算占用的一致性。
Docs Sync: 本卡按现有契约验证。若取证表明契约需调整，先更新后续卡范围。
Done Definition: 完成下列验收和验证，记录证据与剩余风险。
Outcome: Close 等待已经进入的写。通过内部锁边界和公开 Stats 对照关闭前后的已跟踪量。

## 证据与影响

- 检查号：28,29,32,34,41。证据状态：补证。
- Stats 显示跟踪量和 Closed 字段，关闭过程仍允许已进入的写完成。 [store/cache/stats.go:17](../../../store/cache/stats.go#L17)
- [审查报告](../../../docs/evidence/stable-quality-review-dee7aa0f.md)

## 验收与验证

- [x] 最终 Close 后统计稳定，校验写入条目与估算占用的一致性。
- [x] 运行以下命令并记录结果、失败路径和未验证范围。修改 Go 文件前完成仓库预检，验证前先执行仓库格式化。

```sh
go test -race -timeout 60s ./store/cache
```

## 实施证据

[TestCloseTracksFinalPendingWriteInStats](../../../store/cache/state_regression_test.go#L212)
