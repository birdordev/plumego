# Card 1844 — 核对 HTTP 状态分类与显式 Error 标志

Milestone: 稳定模块质量复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: metrics
Owned Files: `metrics/collector_test.go`
Depends On: 无

Goal: ErrorRecords 与 TotalRecords 精确，无重复累加。
Scope: 给收集器人工 2xx、4xx 与 Error 标志组合。
Non-goals: 维持稳定公开签名与标准库依赖，将验证限于本卡范围。
Files: `metrics/collector_test.go`。
Tests: ErrorRecords 与 TotalRecords 精确，无重复累加。
Docs Sync: 复核本卡涉及的已公开约定。
Done Definition: 完成下列验收，记录验证命令、状态及剩余限制。
Outcome: HTTP 错误状态及显式 Error 合并计数，非 HTTP 记录不按状态标签计错。

## 证据与影响

第 9 节检查号为 6,22,44,45。证据状态为补证。

结构化记录可以带 Error，HTTP 观察还按状态判断。区分 err 和状态标记同时存在时的计数。 [metrics/collector.go:228](../../../metrics/collector.go#L228)

[复审报告](../../../docs/evidence/stable-quality-review-a074eadb.md)

## 验收与验证

- [x] ErrorRecords 与 TotalRecords 精确，无重复累加。
- [x] 记录运行结果。若已有用例覆盖目标，记录用例及相关断言。

```sh
go test -race -timeout 60s ./metrics
```

## 实施证据

- [TestHTTPRecordCombinesStatusAndExplicitErrorOnce](../../../metrics/collector_test.go#L278)

[批次验证与基准](../review-a074eadb/README.md)
