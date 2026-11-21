# Card 2070 — 部分启动回滚后注册保持冻结

Milestone: 稳定模块第 9 节复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: core
Owned Files: `core/lifecycle_group_test.go`
Depends On: 无

Goal: 部分启动回滚后注册保持冻结
Scope: 让后一个 Starter 返回哨兵，尝试追加组件，调用 Stop 和 Ready。
Non-goals: 范围限于本卡涉及的契约，保持公开签名和依赖。
Files: `core/lifecycle_group_test.go`。
Tests: 初次回滚按倒序执行，新组件从未启动，已清理项不会再次 Stop。
Docs Sync: 核对新增验收与已有模块说明一致，记录证据和限制。
Done Definition: 完成以下验收，记录命令、结果与剩余限制。
Outcome: 复用既有回归。失败后的倒序清理、重复 Stop 无操作、Ready 拒绝和追加注册不生效均通过。

## 证据与影响

证据状态为补证。第 9 节检查号为 28,30,36,46。 [质量规则第 9 节](../../../docs/operations/agent-code-quality-rules.md#L267)

Start 尝试后阶段转为 inactive，Add 只接收 idle。已有顺序测试外还需验证失败后追加注册的影响。 [core/lifecycle_group.go:134](../../../core/lifecycle_group.go#L134)

[复审报告](../../../docs/evidence/stable-quality-review-2f962d21.md)

## 验收与验证

- [x] 初次回滚按倒序执行，新组件从未启动，已清理项不会再次 Stop。
- [x] 已有测试覆盖相同触发和断言时可直接复用，记录具体用例。

```sh
go test -race -timeout 60s ./core
```

## 实施证据

- [TestLifecycleGroupRollsBackOnStartFailure](../../../core/lifecycle_group_test.go#L144)
