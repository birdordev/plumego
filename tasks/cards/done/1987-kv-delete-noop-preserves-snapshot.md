# Card 1987 — 核对删除不存在条目时的快照身份

Milestone: 稳定模块质量复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: store/kv
Owned Files: `store/kv/state_regression_test.go`
Depends On: 无

Goal: 保留已有数据，明确原路径是否替换，以及取消时的返回值。
Scope: 创建持久化存储和旁边可观察的快照，删除缺失键并重开。
Non-goals: 维持稳定公开签名与标准库依赖，将验证限于本卡范围。
Files: `store/kv/state_regression_test.go`。
Tests: 保留已有数据，明确原路径是否替换，以及取消时的返回值。
Docs Sync: 记录核验结论；只在现有契约文字需要澄清时另提文档变更。
Done Definition: 完成下列验收，记录验证命令、状态及剩余限制。
Outcome: 预取消与缺失删除返回各自分类，状态文件保持同一身份，重开仍可读取原条目。

## 证据与影响

第 9 节检查号为 28,31,34,41，当前证据状态为补证。 [质量规则第 9 节](../../../docs/operations/agent-code-quality-rules.md#L267)

DeleteContext 在内存查找后决定持久化。不存在重复删除是否触发多余写入可用普通文件直测。 [store/kv/kv.go:210](../../../store/kv/kv.go#L210)

[复审报告](../../../docs/evidence/stable-quality-review-999d5e4d.md)

## 验收与验证

- [x] 保留已有数据，明确原路径是否替换，以及取消时的返回值。
- [x] 记录挂载入口、具体断言和结果。已有测试能覆盖时复用并注明。

```sh
go test -race -timeout 60s ./store/kv
```

## 实施证据

- [TestMissingDeletePreservesSnapshotFileAndReopen](../../../store/kv/state_regression_test.go#L950)
