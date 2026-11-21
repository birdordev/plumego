# Card 2098 — KV 过期读统计与文件所有权

Milestone: 稳定模块第 9 节复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: store/kv
Owned Files: `store/kv/state_regression_test.go`
Depends On: 无

Goal: KV 过期读统计与文件所有权
Scope: 固定时钟创建活键和短期键，模拟一次命中、一次过期读、一次普通缺失，再重开。
Non-goals: 范围限于本卡涉及的契约，保持公开签名和依赖。
Files: `store/kv/state_regression_test.go`。
Tests: 本实例条目、字节、比率符合约定，重开从新读计数开始，纯读无磁盘写入。
Docs Sync: 核对新增验收与已有模块说明一致，记录证据和限制。
Done Definition: 完成以下验收，记录命令、结果与剩余限制。
Outcome: 过期与不存在分别记为 miss，命中和剩余占用正确。纯读磁盘不写回，重开读统计从零开始。

## 证据与影响

证据状态为补证。第 9 节检查号为 28,31,32,41,46。 [质量规则第 9 节](../../../docs/operations/agent-code-quality-rules.md#L267)

Stats 同一时点统计存活条目，读计数另行采样且不写盘。 [store/kv/kv.go:356](../../../store/kv/kv.go#L356)

[复审报告](../../../docs/evidence/stable-quality-review-2f962d21.md)

## 验收与验证

- [x] 本实例条目、字节、比率符合约定，重开从新读计数开始，纯读无磁盘写入。
- [x] 已有测试覆盖相同触发和断言时可直接复用，记录具体用例。

```sh
go test -race -timeout 60s ./store/kv
```

## 实施证据

- [TestExpiryReadCountersAreLocalAndDoNotPersist](../../../store/kv/state_regression_test.go#L1108)
