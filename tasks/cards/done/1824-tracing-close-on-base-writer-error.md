# Card 1824 — 核对底层写错误后的 span 收尾参数

Milestone: 稳定模块质量复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: middleware
Owned Files: `middleware/tracing/tracing_regression_test.go`
Depends On: 无

Goal: span 只结束一次，状态和实际写入字节准确。
Scope: 自定义 writer 只接收部分普通正文并返回 I/O 错误。
Non-goals: 维持稳定公开签名与标准库依赖，将验证限于本卡范围。
Files: `middleware/tracing/tracing_regression_test.go`。
Tests: span 只结束一次，状态和实际写入字节准确。
Docs Sync: 复核本卡涉及的已公开约定。
Done Definition: 完成下列验收，记录验证命令、状态及剩余限制。
Outcome: 部分写入后错误退出仍仅发布一条 tracing 记录。

## 证据与影响

第 9 节检查号为 23,29,33,39。证据状态为补证。

trace 结束从 recorder 获取状态与字节。短写与底层失败组合需要精确观察。 [middleware/tracing/tracing.go:37](../../../middleware/tracing/tracing.go#L37)

[复审报告](../../../docs/evidence/stable-quality-review-a074eadb.md)

## 验收与验证

- [x] span 只结束一次，状态和实际写入字节准确。
- [x] 记录运行结果。若已有用例覆盖目标，记录用例及相关断言。

```sh
go test -race -timeout 60s ./middleware/tracing
```

## 实施证据

- [TestTracingRecordsPartialWriteOnErrorExactlyOnce](../../../middleware/tracing/tracing_regression_test.go#L92)

[批次验证与基准](../review-a074eadb/README.md)
