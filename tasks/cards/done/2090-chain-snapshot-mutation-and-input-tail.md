# Card 2090 — Chain 快照顺序与原切片隔离

Milestone: 稳定模块第 9 节复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P3
State: done
Primary Module: middleware
Owned Files: `middleware/middleware_test.go`
Depends On: 无

Goal: Chain 快照顺序与原切片隔离
Scope: 以来自不同作用域的两个普通中间件构造和复制快照，再替换其中一项。
Non-goals: 范围限于本卡涉及的契约，保持公开签名和依赖。
Files: `middleware/middleware_test.go`。
Tests: 后续 Build 次序及原来 next 次数保持，原输入相互独立。
Docs Sync: 核对新增验收与已有模块说明一致，记录证据和限制。
Done Definition: 完成以下验收，记录命令、结果与剩余限制。
Outcome: 扩充原快照回归到两个中间件。修改输入切片尾项或快照首项均不改变 Build 链次序。

## 证据与影响

证据状态为补证。第 9 节检查号为 5,20,34,39。 [质量规则第 9 节](../../../docs/operations/agent-code-quality-rules.md#L267)

链条持有顺序，Snapshot 返回后调用方可改写切片。 [middleware/middleware.go:109](../../../middleware/middleware.go#L109)

[复审报告](../../../docs/evidence/stable-quality-review-2f962d21.md)

## 验收与验证

- [x] 后续 Build 次序及原来 next 次数保持，原输入相互独立。
- [x] 已有测试覆盖相同触发和断言时可直接复用，记录具体用例。

```sh
go test -race -timeout 60s ./middleware
```

## 实施证据

- [TestChainSnapshotReturnsCopy](../../../middleware/middleware_test.go#L88)
- [TestChainBuildIsImmutableSnapshot](../../../middleware/middleware_test.go#L75)
