# Card 1868 — 核对 ErrorBuilder 设定的 RequestID 优先级及降级

Milestone: 稳定模块质量复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: contract
Owned Files: `contract/response_test.go`
Depends On: 无

Goal: 只输出规范 ID，回退规则和错误类别稳定。
Scope: 用合法显式 ID、拒绝的值及空值三组，注入不同请求上下文 ID，比较状态和 JSON。
Non-goals: 维持稳定公开签名与标准库依赖，将验证限于本卡范围。
Files: `contract/response_test.go`。
Tests: 只输出规范 ID，回退规则和错误类别稳定。
Docs Sync: 记录核验结论；只在现有契约文字需要澄清时另提文档变更。
Done Definition: 完成下列验收，记录验证命令、状态及剩余限制。
Outcome: 规范显式 ID 优先，不合法或缺失时回退到请求 Context 的 ID，错误状态保持。

## 证据与影响

第 9 节检查号为 6,21,25,27,38。证据状态为补证。

错误 ID 优先采用错误持有值，其次请求上下文。格式不合法会在归一化时清除，组合需验证。 [contract/response.go:121](../../../contract/response.go#L121)

[复审报告](../../../docs/evidence/stable-quality-review-a8c2f28a.md)

## 验收与验证

- [x] 只输出规范 ID，回退规则和错误类别稳定。
- [x] 记录挂载入口、具体断言和结果。已有测试能覆盖时复用并注明。

```sh
go test -race -timeout 60s ./contract
```

## 实施证据

- [TestExplicitErrorIDOrValidatedRequestFallback](../../../contract/response_test.go#L866)

[批次验证与基准](../review-a8c2f28a/README.md)
