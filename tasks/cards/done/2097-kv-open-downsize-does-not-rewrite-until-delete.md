# Card 2097 — 小容量重开后的纯读和下一次删除

Milestone: 稳定模块第 9 节复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: store/kv
Owned Files: `store/kv/state_regression_test.go`
Depends On: 无

Goal: 小容量重开后的纯读和下一次删除
Scope: 固定三个普通键的写入顺序，用更小上限重开，先只读后删除一个保留键。
Non-goals: 范围限于本卡涉及的契约，保持公开签名和依赖。
Files: `store/kv/state_regression_test.go`。
Tests: 只读时磁盘字节不变，删除提交后的再次重开与当前快照一致。
Docs Sync: 核对新增验收与已有模块说明一致，记录证据和限制。
Done Definition: 完成以下验收，记录命令、结果与剩余限制。
Outcome: 按较小容量重开只裁剪内存，原磁盘字节保持。删除一次后按大容量重开仅看到剩余键。

## 证据与影响

证据状态为补证。第 9 节检查号为 28,31,35,41,46。 [质量规则第 9 节](../../../docs/operations/agent-code-quality-rules.md#L267)

打开时按配置收缩内存状态，约定直到调用方下次修改才持久化。 [store/kv/kv.go:103](../../../store/kv/kv.go#L103)

[复审报告](../../../docs/evidence/stable-quality-review-2f962d21.md)

## 验收与验证

- [x] 只读时磁盘字节不变，删除提交后的再次重开与当前快照一致。
- [x] 已有测试覆盖相同触发和断言时可直接复用，记录具体用例。

```sh
go test -race -timeout 60s ./store/kv
```

## 实施证据

- [TestDownsizedOpenLeavesSnapshotUntilDelete](../../../store/kv/state_regression_test.go#L1058)
