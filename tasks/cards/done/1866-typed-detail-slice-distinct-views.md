# Card 1866 — 核对共享底层数组的有类型切片在 Error 详情中的视图

Milestone: 稳定模块质量复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: contract
Owned Files: `contract/errors_test.go`
Depends On: 无

Goal: 两个视图形状和内容独立可见，调用方修改快照不污染源或下一次响应。
Scope: 同一合成数组派生两个有类型切片，比较两份 Details 与 WriteError 中的长度和内容。
Non-goals: 维持稳定公开签名与标准库依赖，将验证限于本卡范围。
Files: `contract/errors_test.go`。
Tests: 两个视图形状和内容独立可见，调用方修改快照不污染源或下一次响应。
Docs Sync: 记录核验结论；只在现有契约文字需要澄清时另提文档变更。
Done Definition: 完成下列验收，记录验证命令、状态及剩余限制。
Outcome: 同源有类型切片视图保持各自长度，详情变更不会污染下一次 JSON 或原数据。

## 证据与影响

第 9 节检查号为 6,33,34,35,38。证据状态为补证。

现有回归覆盖重复共享容器。还需组合起始地址相同但长度不同的两个可序列化切片视图。 [contract/errors.go:593](../../../contract/errors.go#L593)

[复审报告](../../../docs/evidence/stable-quality-review-a8c2f28a.md)

## 验收与验证

- [x] 两个视图形状和内容独立可见，调用方修改快照不污染源或下一次响应。
- [x] 记录挂载入口、具体断言和结果。已有测试能覆盖时复用并注明。

```sh
go test -race -timeout 60s ./contract
```

## 实施证据

- [TestTypedDetailSliceViewsKeepTheirLengths](../../../contract/errors_test.go#L1258)

[批次验证与基准](../review-a8c2f28a/README.md)
