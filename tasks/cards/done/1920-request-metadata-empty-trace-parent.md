# Card 1920 — 核对 TraceContext 的 ParentSpanID 可选指针复制

Milestone: 稳定模块质量复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P3
State: done
Primary Module: contract
Owned Files: `contract/trace_test.go`
Depends On: 无

Goal: 原始、两个 carrier 及空指针各自正确，字段验证保持。
Scope: 用两个 Context 保存同源 parent 指针，修改原变量和一份读回副本，再查另一上下文。
Non-goals: 维持稳定公开签名与标准库依赖，将验证限于本卡范围。
Files: `contract/trace_test.go`。
Tests: 原始、两个 carrier 及空指针各自正确，字段验证保持。
Docs Sync: 记录核验结论；只在现有契约文字需要澄清时另提文档变更。
Done Definition: 完成下列验收，记录验证命令、状态及剩余限制。
Outcome: 可选 ParentSpanID 在存入与读取时均独立复制，有无 Baggage 和缺失父指针保持各自形状。

## 证据与影响

第 9 节检查号为 6,16,27,34,38。证据状态为补证。

追踪 carrier 已覆盖 baggage 独立性。ParentSpanID 可选且同时有指针读写，需与有无 Baggage 两种形状联合核对。 [contract/trace.go:79](../../../contract/trace.go#L79)

[复审报告](../../../docs/evidence/stable-quality-review-2b94cf86.md)

## 验收与验证

- [x] 原始、两个 carrier 及空指针各自正确，字段验证保持。
- [x] 记录挂载入口、具体断言和结果。已有测试能覆盖时复用并注明。

```sh
go test -race -timeout 60s ./contract
```

## 实施证据

- [TestTraceParentPointerOwnedIndependentlyOfBaggage](../../../contract/trace_test.go#L302)

[批次验证与基准](../review-2b94cf86/README.md)
