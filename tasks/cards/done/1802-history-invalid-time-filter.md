# Card 1802 — 历史导出忽略无法解析的时间参数

Milestone: 稳定模块质量复审
Recipe: specs/change-recipes/fix-bug.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: x/observability
Owned Files: `x/observability/ops/healthhttp/history.go`, `x/observability/ops/healthhttp/history_test.go`, `docs/modules/x/observability/ops/README.md`
Depends On: 无

Goal: 无效起止时间均为固定 400，无原输入回显，合法窗口和缺省查询保持。
Scope: 两个字段复用既有参数错误路径，校验合法范围并保持缺省查询。
Non-goals: 维持稳定公开签名与标准库依赖，将验证限于本卡范围。
Files: `x/observability/ops/healthhttp/history.go`, `x/observability/ops/healthhttp/history_test.go`, `docs/modules/x/observability/ops/README.md`。
Tests: 无效起止时间均为固定 400，无原输入回显，合法窗口和缺省查询保持。
Docs Sync: 复核模块说明并验证生成数据。
Done Definition: 完成下列验收，记录验证命令、状态及剩余限制。
Outcome: 历史导出拒绝非法 RFC3339 起止时间和倒序窗口。重新以基线覆盖单文件验证导出入口，明确普通历史列表的范围。

## 证据与影响

第 9 节检查号为 18,21,22,38,46。证据状态为已复现。

显式 start_time 非法时公开历史导出入口返回 200，解析放弃过滤。end_time 使用相同结构。调用方可能接收到超出原时间范围的结果。 [x/observability/ops/healthhttp/history.go:75](../../../x/observability/ops/healthhttp/history.go#L75)

[复审报告](../../../docs/evidence/stable-quality-review-a074eadb.md)

## 验收与验证

- [x] 无效起止时间均为固定 400，无原输入回显，合法窗口和缺省查询保持。
- [x] 记录运行结果。若已有用例覆盖目标，记录用例及相关断言。

```sh
go test -race -timeout 60s ./x/observability/ops/healthhttp
make website-sync
make website-check-generated
```

## 实施证据

- [TestAcceptanceHistoryTimestampFiltersRejectInvalidWindows](../../../x/observability/ops/healthhttp/history_test.go#L251)

[批次验证与基准](../review-a074eadb/README.md)
