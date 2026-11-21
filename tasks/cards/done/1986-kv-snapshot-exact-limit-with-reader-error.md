# Card 1986 — 核对快照读取边界上的底层错误归属

Milestone: 稳定模块质量复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: store/kv
Owned Files: `store/kv/state_regression_test.go`
Depends On: 无

Goal: 底层错误、预算错误、正常 EOF 有可验证的分类。
Scope: 用正常短数据和报告哨兵错误的 reader 观察准确上限及前后一个字节。
Non-goals: 维持稳定公开签名与标准库依赖，将验证限于本卡范围。
Files: `store/kv/state_regression_test.go`。
Tests: 底层错误、预算错误、正常 EOF 有可验证的分类。
Docs Sync: 记录核验结论；只在现有契约文字需要澄清时另提文档变更。
Done Definition: 完成下列验收，记录验证命令、状态及剩余限制。
Outcome: 快照限长读取分清正常 EOF、超预算和底层错误，准确边界两侧的结果已固定。

## 证据与影响

第 9 节检查号为 15,21,35,41,46，当前证据状态为补证。 [质量规则第 9 节](../../../docs/operations/agent-code-quality-rules.md#L267)

快照通过限长 reader 读取。长度等于上限时，底层返回读取错误与正常 EOF 是不同输入。 [store/kv/persistence.go:98](../../../store/kv/persistence.go#L98)

[复审报告](../../../docs/evidence/stable-quality-review-999d5e4d.md)

## 验收与验证

- [x] 底层错误、预算错误、正常 EOF 有可验证的分类。
- [x] 记录挂载入口、具体断言和结果。已有测试能覆盖时复用并注明。

```sh
go test -race -timeout 60s ./store/kv
```

## 实施证据

- [TestReadSnapshotAtLimitPreservesReaderError](../../../store/kv/state_regression_test.go#L934)
