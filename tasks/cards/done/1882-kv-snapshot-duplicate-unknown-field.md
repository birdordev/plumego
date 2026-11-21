# Card 1882 — 核对 KV 快照对未知字段及重复成员的兼容边界

Milestone: 稳定模块质量复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: store
Owned Files: `store/kv/state_regression_test.go`
Depends On: 无

Goal: 合法历史格式可读，重复成员分类为 ErrCorruptState，并可在更换文件后正常重开。
Scope: 临时目录写少量合成快照，比较可前向读取与应拒绝的重复字段，然后检查目录已释放。
Non-goals: 维持稳定公开签名与标准库依赖，将验证限于本卡范围。
Files: `store/kv/state_regression_test.go`。
Tests: 合法历史格式可读，重复成员分类为 ErrCorruptState，并可在更换文件后正常重开。
Docs Sync: 记录核验结论；只在现有契约文字需要澄清时另提文档变更。
Done Definition: 完成下列验收，记录验证命令、状态及剩余限制。
Outcome: 快照接受单个未来字段，拒绝重复未知字段及大小写等价已知字段。修复文件后可重开。

## 证据与影响

第 9 节检查号为 6,18,22,41。证据状态为补证。

快照允许未识别字段随解码忽略，成员去重按结构或字面键分类。需要组合大小写已知字段和普通未来字段。 [store/kv/persistence.go:194](../../../store/kv/persistence.go#L194)

[复审报告](../../../docs/evidence/stable-quality-review-a8c2f28a.md)

## 验收与验证

- [x] 合法历史格式可读，重复成员分类为 ErrCorruptState，并可在更换文件后正常重开。
- [x] 记录挂载入口、具体断言和结果。已有测试能覆盖时复用并注明。

```sh
go test -race -timeout 60s ./store/kv
```

## 实施证据

- [TestKVSnapshotAcceptsSingleUnknownFieldAndRejectsDuplicates](../../../store/kv/state_regression_test.go#L747)

[批次验证与基准](../review-a8c2f28a/README.md)
