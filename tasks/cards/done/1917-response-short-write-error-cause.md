# Card 1917 — 核对编码成功后 writer 的短写与原始错误优先级

Milestone: 稳定模块质量复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: contract
Owned Files: `contract/response_test.go`
Depends On: 无

Goal: 确定最终 Content-Type 和状态，原始错误可分类，无重复写头。
Scope: 两个能够记录提交次数的 writer 分别短写和同时携带原因，对 WriteResponse 和 WriteError 运行。
Non-goals: 维持稳定公开签名与标准库依赖，将验证限于本卡范围。
Files: `contract/response_test.go`。
Tests: 确定最终 Content-Type 和状态，原始错误可分类，无重复写头。
Docs Sync: 记录核验结论；只在现有契约文字需要澄清时另提文档变更。
Done Definition: 完成下列验收，记录验证命令、状态及剩余限制。
Outcome: 成功和错误编码共用提交行为，无原因短写返回 io.ErrShortWrite，有原因则保留底层错误，各一次写头。

## 证据与影响

第 9 节检查号为 21,22,23,38。证据状态为补证。

写入短且无错误时使用 io.ErrShortWrite，底层有原因时原样返回。成功与错误入口拥有同一提交 helper，需联测。 [contract/response.go:69](../../../contract/response.go#L69)

[复审报告](../../../docs/evidence/stable-quality-review-2b94cf86.md)

## 验收与验证

- [x] 确定最终 Content-Type 和状态，原始错误可分类，无重复写头。
- [x] 记录挂载入口、具体断言和结果。已有测试能覆盖时复用并注明。

```sh
go test -race -timeout 60s ./contract
```

## 实施证据

- [TestEncodedResponseKeepsOriginalErrorOverShortWrite](../../../contract/response_test.go#L896)

[批次验证与基准](../review-2b94cf86/README.md)
