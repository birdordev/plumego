# Card 2046 — 快照写失败时保留 Close 错误分类

Milestone: 稳定模块第 9 节复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: store/kv
Owned Files: `store/kv/state_regression_test.go`
Depends On: 无

Goal: 快照写失败时保留 Close 错误分类
Scope: 使用本机注入文件 doubles 验证写失败、关闭失败与替换调用记录。
Non-goals: 范围限于本卡涉及的契约，保持公开签名和依赖。
Files: `store/kv/state_regression_test.go`。
Tests: 两个哨兵都可分类，未发生发布，清理路径可重复执行。
Docs Sync: 核对新增验收与已有模块说明一致，记录证据和限制。
Done Definition: 完成以下验收，记录命令、结果与剩余限制。
Outcome: 复用文件桩。快照写失败和关闭失败均保留分类，原快照不变。临时故障排除后可重试。

## 证据与影响

证据状态为补证。第 9 节检查号为 22,29,30,31,41,46。 [质量规则第 9 节](../../../docs/operations/agent-code-quality-rules.md#L267)

临时快照写入失败后 Close 也可能失败，当前 helper 单独合并两个错误。 [store/kv/persistence.go:270](../../../store/kv/persistence.go#L270)

[复审报告](../../../docs/evidence/stable-quality-review-c3a77410.md)

## 验收与验证

- [x] 两个哨兵都可分类，未发生发布，清理路径可重复执行。
- [x] 已有测试覆盖相同触发和断言时可直接复用，记录具体用例。

```sh
go test -race -timeout 60s ./store/kv
```

## 实施证据

- [TestSnapshotWritePreservesCleanupErrors](../../../store/kv/state_regression_test.go#L450)
- [TestWriteFailureRestoresEvictionAndAllowsRetry](../../../store/kv/state_regression_test.go#L129)
