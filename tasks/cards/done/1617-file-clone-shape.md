# Card 1617 — 补证文件元数据克隆的共享引用语义

Milestone: 本轮质量审查
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: store
Owned Files: `store/file/cycle_regression_test.go`
Depends On: 无

Goal: 补证文件元数据克隆的共享引用语义。
Scope: 复用克隆用例，构造普通的共享子对象。
Non-goals: 保留现有公开签名、依赖与模块边界。测试若证实新的缺陷，先记录证据再单独安排修复。
Files: `store/file/cycle_regression_test.go`。
Tests: Clone、CloneMetadata 对原输入隔离，记录快照内重复引用是否共享。
Docs Sync: 本卡按现有契约验证。若取证表明契约需调整，先更新后续卡范围。
Done Definition: 完成下列验收和验证，记录证据与剩余风险。
Outcome: File.Clone 和 PutOptions.CloneMetadata 的递归容器与源隔离，同时保留副本内部共享引用。

## 证据与影响

- 检查号：16,34,41,46。证据状态：补证。
- 循环容器已有专项修复，跨子字段重复引用仍需确认是否保留别名及隔离源数据。 [store/file/types.go:99](../../../store/file/types.go#L99)
- [审查报告](../../../docs/evidence/stable-quality-review-dee7aa0f.md)

## 验收与验证

- [x] Clone、CloneMetadata 对原输入隔离，记录快照内重复引用是否共享。
- [x] 运行以下命令并记录结果、失败路径和未验证范围。修改 Go 文件前完成仓库预检，验证前先执行仓库格式化。

```sh
go test -race -timeout 60s ./store/file
```

## 实施证据

[TestMetadataClonePreservesSharedContainerWithinSnapshot](../../../store/file/cycle_regression_test.go#L88)
