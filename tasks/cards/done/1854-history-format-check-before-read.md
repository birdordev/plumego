# Card 1854 — 不支持的导出格式仍查询整个历史

Milestone: 稳定模块质量复审
Recipe: specs/change-recipes/fix-bug.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: x/observability/ops
Owned Files: `x/observability/ops/healthhttp/history.go`, `x/observability/ops/healthhttp/history_test.go`
Depends On: 1851

Goal: 组合不合法查询与格式验证错误顺序。对大小历史比较拒绝路径分配，JSON 和 CSV 输出不变。
Scope: 保留现有参数错误优先级，完成有效格式判定后才访问历史。
Non-goals: 维持稳定公开签名与标准库依赖，将验证限于本卡范围。
Files: `x/observability/ops/healthhttp/history.go`, `x/observability/ops/healthhttp/history_test.go`。
Tests: 组合不合法查询与格式验证错误顺序。对大小历史比较拒绝路径分配，JSON 和 CSV 输出不变。
Docs Sync: 记录核验结论；只在现有契约文字需要澄清时另提文档变更。
Done Definition: 完成下列验收，记录验证命令、状态及剩余限制。
Outcome: 格式检查先于历史查询，分页等参数仍优先验证；同一基准比较基线和当前的拒绝路径分配。

## 证据与影响

第 9 节检查号为 10,18,35,42,48。证据状态为源码确认。

处理器先过滤和构造分页结果，再进入 format 分支；无法导出的格式执行了全部查询工作。大历史会占用读锁并分配临时切片。 [x/observability/ops/healthhttp/history.go:52](../../../x/observability/ops/healthhttp/history.go#L52)

[复审报告](../../../docs/evidence/stable-quality-review-a8c2f28a.md)

## 验收与验证

- [x] 组合不合法查询与格式验证错误顺序。对大小历史比较拒绝路径分配，JSON 和 CSV 输出不变。
- [x] 记录挂载入口、具体断言和结果。已有测试能覆盖时复用并注明。

```sh
go test -race -timeout 60s ./x/observability/ops/healthhttp
go test ./x/observability/ops/healthhttp -run '^$' -bench '^BenchmarkHistoryUnsupportedFormat$' -benchmem -benchtime=100ms -count=3
```

## 实施证据

- [TestAcceptanceHistoryExportValidatesPaginationBeforeFormat](../../../x/observability/ops/healthhttp/history_test.go#L321)
- [BenchmarkHistoryUnsupportedFormat](../../../x/observability/ops/healthhttp/history_test.go#L362)

[批次验证与基准](../review-a8c2f28a/README.md)
