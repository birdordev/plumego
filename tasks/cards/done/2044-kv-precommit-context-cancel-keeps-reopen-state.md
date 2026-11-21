# Card 2044 — KV 预取消写入保留持久快照

Milestone: 稳定模块第 9 节复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: store/kv
Owned Files: `store/kv/state_regression_test.go`
Depends On: 无

Goal: KV 预取消写入保留持久快照
Scope: 已写一条记录后取消下一次普通替换，分别观察当前实例和重开实例。
Non-goals: 范围限于本卡涉及的契约，保持公开签名和依赖。
Files: `store/kv/state_regression_test.go`。
Tests: 两处字节相同，Stats 保持已有数量，返回 context.Canceled。
Docs Sync: 核对新增验收与已有模块说明一致，记录证据和限制。
Done Definition: 完成以下验收，记录命令、结果与剩余限制。
Outcome: 预取消替换返回 context.Canceled，原实例和重开实例均只含原持久值。

## 证据与影响

证据状态为补证。第 9 节检查号为 26,31,34,41,46。 [质量规则第 9 节](../../../docs/operations/agent-code-quality-rules.md#L267)

SetContext 与磁盘快照关系跨越内存修改和替换提交。需要公开入口取消后重开对照。 [store/kv/kv.go:113](../../../store/kv/kv.go#L113)

[复审报告](../../../docs/evidence/stable-quality-review-c3a77410.md)

## 验收与验证

- [x] 两处字节相同，Stats 保持已有数量，返回 context.Canceled。
- [x] 已有测试覆盖相同触发和断言时可直接复用，记录具体用例。

```sh
go test -race -timeout 60s ./store/kv
```

## 实施证据

- [TestPreCanceledReplacementLeavesSameRecordAfterReopen](../../../store/kv/state_regression_test.go#L991)
