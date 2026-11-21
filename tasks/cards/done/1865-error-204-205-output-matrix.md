# Card 1865 — 核对协议无正文状态与错误响应形状

Milestone: 稳定模块质量复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: contract
Owned Files: `contract/response_test.go`
Depends On: 无

Goal: 无正文状态不会编码或写体，错误入口依原规则保持规范的失败状态。
Scope: 用独立 recorder 分别断言状态、长度、已有业务头、encoder 调用次数和错误外壳。
Non-goals: 维持稳定公开签名与标准库依赖，将验证限于本卡范围。
Files: `contract/response_test.go`。
Tests: 无正文状态不会编码或写体，错误入口依原规则保持规范的失败状态。
Docs Sync: 记录核验结论；只在现有契约文字需要澄清时另提文档变更。
Done Definition: 完成下列验收，记录验证命令、状态及剩余限制。
Outcome: 204、205 即使数据无法编码也不写正文，情况各自清理长度头，非法错误状态回落为规范失败。

## 证据与影响

第 9 节检查号为 6,21,23,24,38。证据状态为补证。

204 和 205 绕过 JSON 编码并处理长度头。需在同一矩阵检查预置内容元数据、抛错 encoder 以及错误入口对状态归一化。 [contract/response.go:22](../../../contract/response.go#L22)

[复审报告](../../../docs/evidence/stable-quality-review-a8c2f28a.md)

## 验收与验证

- [x] 无正文状态不会编码或写体，错误入口依原规则保持规范的失败状态。
- [x] 记录挂载入口、具体断言和结果。已有测试能覆盖时复用并注明。

```sh
go test -race -timeout 60s ./contract
```

## 实施证据

- [TestBodylessStatusesSkipEncodingWhileNormalizingErrors](../../../contract/response_test.go#L842)

[批次验证与基准](../review-a8c2f28a/README.md)
