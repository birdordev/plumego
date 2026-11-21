# Card 1655 — 测量追踪 baggage 的复制与大字段成本

Milestone: 稳定模块质量复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: contract
Owned Files: `contract/trace_test.go`
Depends On: 无

Goal: 测量追踪 baggage 的复制与大字段成本。
Scope: 加入小和中等样本的基准，只测现有契约。
Non-goals: 维持公开签名和依赖；涉及现有兼容契约的调整须先核对调用边界及迁移影响。
Files: `contract/trace_test.go`。
Tests: 记录单次分配并同时断言副本隔离。
Docs Sync: 对照原契约核验，若证据要求修订公开行为，先更新实施范围。
Done Definition: 完成测试与验收记录，并标明剩余取证范围。
Outcome: 测量 2 项及 64 项 trace baggage 写入和读取成本，确认副本与原有数据隔离。

## 证据

检查号为 27,34,35,48，状态为补证。

上下文写入与读取均复制 baggage，调用频率和字段数量共同影响分配。 [contract/trace.go:79](../../../contract/trace.go#L79)

[复审报告](../../../docs/evidence/stable-quality-review-09a7f5d3.md)

## 验收与验证

- [x] 记录单次分配并同时断言副本隔离。
- [x] 记录验证命令、状态和剩余范围。若使用既有用例满足验收，记录对应证据。

```sh
go test -race -timeout 60s ./contract
go test ./contract -run '^$' -bench '^BenchmarkTraceBaggageCopies$' -benchmem -count=3
```

## 实施证据

[BenchmarkTraceBaggageCopies](../../../contract/trace_test.go#L245) · [TestTraceContextUsesDefensiveCopies](../../../contract/trace_test.go#L126)
