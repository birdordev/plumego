# Card 1814 — 补证 HEAD 延迟提交时的头部快照

Milestone: 稳定模块质量复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: router
Owned Files: `router/head_regression_test.go`
Depends On: 无

Goal: 最终响应头按提交时序，正文为空。
Scope: 用 recorder 或真实 loopback 只测试公开 handler。
Non-goals: 维持稳定公开签名与标准库依赖，将验证限于本卡范围。
Files: `router/head_regression_test.go`。
Tests: 最终响应头按提交时序，正文为空。
Docs Sync: 复核本卡涉及的已公开约定。
Done Definition: 完成下列验收，记录验证命令、状态及剩余限制。
Outcome: 信息响应与最终头部是独立快照，提交前后的变更按阶段处理。

## 证据与影响

第 9 节检查号为 6,23,24,37。证据状态为补证。

HEAD 回放把状态和响应头分开保存。提前写头后修改、一次信息头和显式 trailers 的组合还未联合核对。 [router/dispatch.go:32](../../../router/dispatch.go#L32)

[复审报告](../../../docs/evidence/stable-quality-review-a074eadb.md)

## 验收与验证

- [x] 最终响应头按提交时序，正文为空。
- [x] 记录运行结果。若已有用例覆盖目标，记录用例及相关断言。

```sh
go test -race -timeout 60s ./router
```

## 实施证据

- [TestHeadInformationAndFinalHeaderSnapshotAreSeparate](../../../router/head_regression_test.go#L492)

[批次验证与基准](../review-a074eadb/README.md)
