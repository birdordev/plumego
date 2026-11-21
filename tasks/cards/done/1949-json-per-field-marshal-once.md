# Card 1949 — 核对自定义 JSON field 编码回调次数与派生隔离

Milestone: 稳定模块质量复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: log
Owned Files: `log/json_test.go`
Depends On: 无

Goal: 编码回调每条记录一次，失败值按固定回退，成功字段及另一条记录不变。
Scope: 普通有计数的 marshaler 与固定错误的 marshaler 作为并列字段，父、子各写一条。
Non-goals: 维持稳定公开签名与标准库依赖，将验证限于本卡范围。
Files: `log/json_test.go`。
Tests: 编码回调每条记录一次，失败值按固定回退，成功字段及另一条记录不变。
Docs Sync: 记录核验结论；只在现有契约文字需要澄清时另提文档变更。
Done Definition: 完成下列验收，记录验证命令、状态及剩余限制。
Outcome: 每个 JSON 字段编码回调每条记录一次。失败 sibling 使用固定回退，其他字段保留整数精度与完整 JSON。

## 证据与影响

第 9 节检查号为 25,33,34,43。证据状态为补证。

JSON 针对单个 field 编码后存为 RawMessage。需要固定 custom marshaler 的调用次数，以及失败值隔离后其他字段仍完好。 [log/json.go:282](../../../log/json.go#L282)

[复审报告](../../../docs/evidence/stable-quality-review-2b94cf86.md)

## 验收与验证

- [x] 编码回调每条记录一次，失败值按固定回退，成功字段及另一条记录不变。
- [x] 记录挂载入口、具体断言和结果。已有测试能覆盖时复用并注明。

```sh
go test -race -timeout 60s ./log
```

## 实施证据

- [TestJSONFieldEncoderRunsOnceAndIsolatesFailedSibling](../../../log/json_test.go#L1005)

[批次验证与基准](../review-2b94cf86/README.md)
