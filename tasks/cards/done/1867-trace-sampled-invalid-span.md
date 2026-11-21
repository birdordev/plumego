# Card 1867 — 核对采样标志与部分有效追踪字段的入口策略

Milestone: 稳定模块质量复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: contract
Owned Files: `contract/trace_test.go`
Depends On: 无

Goal: 有效性与采样各按字段约定判断，两个请求上下文独立。
Scope: 测试无 SpanID、混合大小写、全零及合法 ID，并验证 Flags 和 baggage 的快照。
Non-goals: 维持稳定公开签名与标准库依赖，将验证限于本卡范围。
Files: `contract/trace_test.go`。
Tests: 有效性与采样各按字段约定判断，两个请求上下文独立。
Docs Sync: 记录核验结论；只在现有契约文字需要澄清时另提文档变更。
Done Definition: 完成下列验收，记录验证命令、状态及剩余限制。
Outcome: 采样标记独立于 ID 的有效性，追踪 carrier 在 Context 的写入和读取边界复制 baggage。

## 证据与影响

第 9 节检查号为 6,18,25,27,38。证据状态为补证。

TraceContext 的采样和有效性是独立方法，Context accessor 也有拷贝路径。仅 TraceID 合法时的行为应明确。 [contract/trace.go:46](../../../contract/trace.go#L46)

[复审报告](../../../docs/evidence/stable-quality-review-a8c2f28a.md)

## 验收与验证

- [x] 有效性与采样各按字段约定判断，两个请求上下文独立。
- [x] 记录挂载入口、具体断言和结果。已有测试能覆盖时复用并注明。

```sh
go test -race -timeout 60s ./contract
```

## 实施证据

- [TestSampledTraceCarrierRetainsPartialAndKnownStates](../../../contract/trace_test.go#L285)

[批次验证与基准](../review-a8c2f28a/README.md)
