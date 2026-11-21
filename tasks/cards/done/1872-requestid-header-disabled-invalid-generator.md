# Card 1872 — 核对无效自定义生成器下的禁写请求头配置

Milestone: 稳定模块质量复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: middleware
Owned Files: `middleware/requestid/request_id_test.go`
Depends On: 无

Goal: 所有合法结果字段一致，原请求和禁写模式的 Header 归属正确，生成器仅调用一次。
Scope: 注入返回不合法普通字面值的 generator，并传入带其他元数据的请求。
Non-goals: 维持稳定公开签名与标准库依赖，将验证限于本卡范围。
Files: `middleware/requestid/request_id_test.go`。
Tests: 所有合法结果字段一致，原请求和禁写模式的 Header 归属正确，生成器仅调用一次。
Docs Sync: 记录核验结论；只在现有契约文字需要澄清时另提文档变更。
Done Definition: 完成下列验收，记录验证命令、状态及剩余限制。
Outcome: 禁用请求头写回时，自定义生成值无效则回退到合规生成值，同时保留原请求及其他头部。

## 证据与影响

第 9 节检查号为 14,20,25,27,39。证据状态为补证。

EnsureRequestID 在自定义值不被接受时回退。禁止给请求副本写头时仍要对齐响应与 ctx。 [middleware/requestid/helpers.go:47](../../../middleware/requestid/helpers.go#L47)

[复审报告](../../../docs/evidence/stable-quality-review-a8c2f28a.md)

## 验收与验证

- [x] 所有合法结果字段一致，原请求和禁写模式的 Header 归属正确，生成器仅调用一次。
- [x] 记录挂载入口、具体断言和结果。已有测试能覆盖时复用并注明。

```sh
go test -race -timeout 60s ./middleware/requestid
```

## 实施证据

- [TestInvalidGeneratedIDFallbackWithoutRequestHeaderWriteback](../../../middleware/requestid/request_id_test.go#L206)

[批次验证与基准](../review-a8c2f28a/README.md)
