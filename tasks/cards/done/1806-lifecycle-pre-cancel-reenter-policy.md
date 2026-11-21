# Card 1806 — 核对生命周期入口检查自定义 Context 的锁边界

Milestone: 稳定模块质量复审
Recipe: specs/change-recipes/analysis-only.yaml
Context Package: review
Priority: P2
State: done
Primary Module: core
Owned Files: `core/lifecycle_group_test.go`
Depends On: 无

Goal: 预取消仍不冻结注册，回调锁边界有明确测试或契约。
Scope: 先核对自定义实现的支持边界；用非阻塞合成 Context 记录阶段观察顺序。
Non-goals: 维持稳定公开签名与标准库依赖，将验证限于本卡范围。
Files: `core/lifecycle_group_test.go`。
Tests: 预取消仍不冻结注册，回调锁边界有明确测试或契约。
Docs Sync: 复核本卡涉及的已公开约定。
Done Definition: 完成下列验收，记录验证命令、状态及剩余限制。
Outcome: 验证入口检查在生命周期锁内读取 Context，已取消尝试不推进阶段且可继续注册。记录自定义 Err 不应同步等待同一组锁的边界。

## 证据与影响

第 9 节检查号为 26,28,33,36。证据状态为补证。

Start 在阶段锁内读取 ctx.Err；自定义 Context 的行为要求未单独验证。 [core/lifecycle_group.go:162](../../../core/lifecycle_group.go#L162)

[复审报告](../../../docs/evidence/stable-quality-review-a074eadb.md)

## 验收与验证

- [x] 预取消仍不冻结注册，回调锁边界有明确测试或契约。
- [x] 记录运行结果。若已有用例覆盖目标，记录用例及相关断言。

```sh
go test -race -timeout 60s ./core
```

## 实施证据

- [TestCanceledStartCheckLeavesLifecycleRegistrationOpen](../../../core/lifecycle_group_test.go#L738)

[批次验证与基准](../review-a074eadb/README.md)
