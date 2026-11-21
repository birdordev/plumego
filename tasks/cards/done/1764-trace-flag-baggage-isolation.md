# Card 1764 — 补证 trace flags 与 baggage 更新时的快照

Milestone: 稳定模块质量复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: contract
Owned Files: `contract/trace_test.go`
Depends On: 无

Goal: 两份上下文互不干扰，并保留 request ID。
Scope: 建立两个衍生 Context，分别改变临时 baggage 和采样值。
Non-goals: 维持稳定公开签名与标准库依赖，将验证限于本卡范围。
Files: `contract/trace_test.go`。
Tests: 两份上下文互不干扰，并保留 request ID。
Docs Sync: 复核本卡涉及的已公开约定。
Done Definition: 完成下列验收，记录验证命令、状态及剩余限制。
Outcome: 同一父请求的两个 trace 快照保留各自采样值和 baggage，父 request_id 仍可读。

## 证据与影响

第 9 节检查号为 27,32,34,38。证据状态为补证。

同一请求可派生多个追踪上下文；读取已复制 baggage，采样标志应只由对应快照决定。 [contract/trace.go:55](../../../contract/trace.go#L55)

[复审报告](../../../docs/evidence/stable-quality-review-d224acbc.md)

## 验收与验证

- [x] 两份上下文互不干扰，并保留 request ID。
- [x] 记录运行结果。若已有用例覆盖目标，记录用例及相关断言。

```sh
go test -race -timeout 60s ./contract
```

## 实施证据

- [TestSiblingTraceSnapshotsKeepSamplingAndBaggageSeparate](../../../contract/trace_test.go#L269)

[批次验证与基准](../review-d224acbc/README.md)
