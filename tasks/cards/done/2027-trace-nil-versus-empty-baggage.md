# Card 2027 — TraceContext 空 Baggage 快照形状

Milestone: 稳定模块第 9 节复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P3
State: done
Primary Module: contract
Owned Files: `contract/trace_test.go`
Depends On: 无

Goal: TraceContext 空 Baggage 快照形状
Scope: 对 nil 与显式空 map 独立往返，再写入取回的非 nil map。
Non-goals: 范围限于本卡涉及的契约，保持公开签名和依赖。
Files: `contract/trace_test.go`。
Tests: 形状按契约保存，读取方写入不改变后续读取。
Docs Sync: 核对新增验收与已有模块说明一致，记录证据和限制。
Done Definition: 完成以下验收，记录命令、结果与剩余限制。
Outcome: 复用 nil 和显式空 baggage 的双向隔离回归，调用与读取方的修改都不改快照。

## 证据与影响

证据状态为补证。第 9 节检查号为 6,27,34,38。 [质量规则第 9 节](../../../docs/operations/agent-code-quality-rules.md#L267)

trace accessor 复制 Baggage，空 map 的零值语义需在输入和输出两端核验。 [contract/trace.go:79](../../../contract/trace.go#L79)

[复审报告](../../../docs/evidence/stable-quality-review-c3a77410.md)

## 验收与验证

- [x] 形状按契约保存，读取方写入不改变后续读取。
- [x] 已有测试覆盖相同触发和断言时可直接复用，记录具体用例。

```sh
go test -race -timeout 60s ./contract
```

## 实施证据

- [TestTraceContextPreservesEmptyBaggageShape](../../../contract/trace_test.go#L165)
