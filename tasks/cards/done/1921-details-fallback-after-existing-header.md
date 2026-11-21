# Card 1921 — 核对失败详情降级与底层业务头和短写的组合

Milestone: 稳定模块质量复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: contract
Owned Files: `contract/response_test.go`
Depends On: 无

Goal: 已提交状态保持原 APIError，闭合外壳省略详情，联合错误均可识别。
Scope: 使用不可编码的普通详情及明确报错的 writer，断言请求 ID、外层业务头和错误分类。
Non-goals: 维持稳定公开签名与标准库依赖，将验证限于本卡范围。
Files: `contract/response_test.go`。
Tests: 已提交状态保持原 APIError，闭合外壳省略详情，联合错误均可识别。
Docs Sync: 记录核验结论；只在现有契约文字需要澄清时另提文档变更。
Done Definition: 完成下列验收，记录验证命令、状态及剩余限制。
Outcome: 详情编码失败回退时保留原业务头、请求 ID 和状态，同时返回编码与 writer 写入两种原因。

## 证据与影响

第 9 节检查号为 21,22,23,38。证据状态为补证。

详情编码失败可退回无详情错误。头已预置时如果写体再失败，需要证实两个原因都保留且只提交一次。 [contract/response.go:136](../../../contract/response.go#L136)

[复审报告](../../../docs/evidence/stable-quality-review-2b94cf86.md)

## 验收与验证

- [x] 已提交状态保持原 APIError，闭合外壳省略详情，联合错误均可识别。
- [x] 记录挂载入口、具体断言和结果。已有测试能覆盖时复用并注明。

```sh
go test -race -timeout 60s ./contract
```

## 实施证据

- [TestErrorDetailFallbackJoinsEncodingAndWriterFailures](../../../contract/response_test.go#L919)

[批次验证与基准](../review-2b94cf86/README.md)
