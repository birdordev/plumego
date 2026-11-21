# Card 2045 — KV Keys 快照及排序与删除隔离

Milestone: 稳定模块第 9 节复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: store/kv
Owned Files: `store/kv/state_regression_test.go`
Depends On: 无

Goal: KV Keys 快照及排序与删除隔离
Scope: 持有一次快照，再通过通道触发删除和插入，随后改写旧快照。
Non-goals: 范围限于本卡涉及的契约，保持公开签名和依赖。
Files: `store/kv/state_regression_test.go`。
Tests: 旧快照由调用方持有，新快照排序且仅含实际存活键。
Docs Sync: 核对新增验收与已有模块说明一致，记录证据和限制。
Done Definition: 完成以下验收，记录命令、结果与剩余限制。
Outcome: 通道确认插入和删除完成后，旧快照仍独立，修改旧切片不影响下一份排序结果。

## 证据与影响

证据状态为补证。第 9 节检查号为 31,32,34,41,47。 [质量规则第 9 节](../../../docs/operations/agent-code-quality-rules.md#L267)

snapshotKeys 在内部读取快照后按公开顺序返回。需在同步写入前后比较返回切片的所有权。 [store/kv/kv.go:294](../../../store/kv/kv.go#L294)

[复审报告](../../../docs/evidence/stable-quality-review-c3a77410.md)

## 验收与验证

- [x] 旧快照由调用方持有，新快照排序且仅含实际存活键。
- [x] 已有测试覆盖相同触发和断言时可直接复用，记录具体用例。

```sh
go test -race -timeout 60s ./store/kv
```

## 实施证据

- [TestKeyListSnapshotSurvivesInsertDeleteAndCallerMutation](../../../store/kv/state_regression_test.go#L1026)
