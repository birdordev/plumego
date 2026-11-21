# Card 1969 — 核对有效无采样 TraceContext 的往返

Milestone: 稳定模块质量复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P3
State: done
Primary Module: contract
Owned Files: `contract/trace_test.go`
Depends On: 无

Goal: 采样结果和上下文有效性独立，后续输入更改不渗入快照。
Scope: 用有效合成 ID、无采样标志、空与非空附加数据做表驱动。
Non-goals: 维持稳定公开签名与标准库依赖，将验证限于本卡范围。
Files: `contract/trace_test.go`。
Tests: 采样结果和上下文有效性独立，后续输入更改不渗入快照。
Docs Sync: 记录核验结论；只在现有契约文字需要澄清时另提文档变更。
Done Definition: 完成下列验收，记录验证命令、状态及剩余限制。
Outcome: 有效且未采样的 trace 保持传播，附加字段已与输入 map 分离。

## 证据与影响

第 9 节检查号为 25,27,31,38，当前证据状态为补证。 [质量规则第 9 节](../../../docs/operations/agent-code-quality-rules.md#L267)

有 trace ID 和 span ID 的无采样上下文仍可有效。显式无采样及附加字段的克隆需要通过公共接口核对。 [contract/trace.go:29](../../../contract/trace.go#L29)

[复审报告](../../../docs/evidence/stable-quality-review-999d5e4d.md)

## 验收与验证

- [x] 采样结果和上下文有效性独立，后续输入更改不渗入快照。
- [x] 记录挂载入口、具体断言和结果。已有测试能覆盖时复用并注明。

```sh
go test -race -timeout 60s ./contract
```

## 实施证据

- [TestUnsampledTraceRetainsValidContextAndBaggage](../../../contract/trace_test.go#L320)
