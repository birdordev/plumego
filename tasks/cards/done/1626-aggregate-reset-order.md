# Card 1626 — 验证多收集器分阶段重置的可观察性

Milestone: 本轮质量审查
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: metrics
Owned Files: `metrics/multi_test.go`
Depends On: 无

Goal: 验证多收集器分阶段重置的可观察性。
Scope: 用受控子收集器在两个 Reset 之间阻塞并取得快照。
Non-goals: 保留现有公开签名、依赖与模块边界。测试若证实新的缺陷，先记录证据再单独安排修复。
Files: `metrics/multi_test.go`。
Tests: 中间结果可解释，最终 Clear 时全部子收集器已清理。
Docs Sync: 本卡按现有契约验证。若取证表明契约需调整，先更新后续卡范围。
Done Definition: 完成下列验收和验证，记录证据与剩余风险。
Outcome: 通道阻塞第二个子收集器重置，聚合读到阶段性剩余计数，完成后全部清空。

## 证据与影响

- 检查号：28,32,44,47。证据状态：补证。
- 聚合读取和重置依次操作各子收集器，文档明确可见中间阶段。 [metrics/multi.go:120](../../../metrics/multi.go#L120)
- [审查报告](../../../docs/evidence/stable-quality-review-dee7aa0f.md)

## 验收与验证

- [x] 中间结果可解释，最终 Clear 时全部子收集器已清理。
- [x] 运行以下命令并记录结果、失败路径和未验证范围。修改 Go 文件前完成仓库预检，验证前先执行仓库格式化。

```sh
go test -race -timeout 60s ./metrics
```

## 实施证据

[TestMultiCollectorClearPublishesEachResetIndependently](../../../metrics/multi_test.go#L456)
