# Card 1851 — 历史导出的无效分页参数悄悄回退到默认值

Milestone: 稳定模块质量复审
Recipe: specs/change-recipes/fix-bug.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: x/observability/ops
Owned Files: `x/observability/ops/healthhttp/history.go`, `x/observability/ops/healthhttp/history_test.go`, `docs/modules/x/observability/ops/README.md`
Depends On: 1850

Goal: 分别验证 limit、offset 的缺省、合法、非整数和超出 int 范围，错误使用同一结构且不回显入参。
Scope: 复用已有 invalidParamError，拒绝非整数或不可表达的分页字面值。保留既有空值和整数范围归一化，另记录如果需要改变整数范围的迁移决策。
Non-goals: 维持稳定公开签名与标准库依赖，将验证限于本卡范围。
Files: `x/observability/ops/healthhttp/history.go`, `x/observability/ops/healthhttp/history_test.go`, `docs/modules/x/observability/ops/README.md`。
Tests: 分别验证 limit、offset 的缺省、合法、非整数和超出 int 范围，错误使用同一结构且不回显入参。
Docs Sync: 行为变更后同步清单对应的模块说明并校验生成数据。
Done Definition: 完成下列验收，记录验证命令、状态及剩余限制。
Outcome: 导出复用规范参数错误拒绝非整数和超出 int 范围的分页值，空值和合法整数继续走原归一化。

## 证据与影响

第 9 节检查号为 14,18,21,22,42。证据状态为已复现。

分页字面值不合法时导出仍返回 200，回退为 limit 50、offset 0，调用方无法发现参数拼写错误。和同入口的时间、状态字段处理不一致。 [x/observability/ops/healthhttp/history.go:101](../../../x/observability/ops/healthhttp/history.go#L101)

[复审报告](../../../docs/evidence/stable-quality-review-a8c2f28a.md)

## 验收与验证

- [x] 分别验证 limit、offset 的缺省、合法、非整数和超出 int 范围，错误使用同一结构且不回显入参。
- [x] 记录挂载入口、具体断言和结果。已有测试能覆盖时复用并注明。

```sh
go test -race -timeout 60s ./x/observability/ops/healthhttp
make website-sync
make website-check-generated
```

## 实施证据

- [TestAcceptanceHistoryExportValidatesPaginationBeforeFormat](../../../x/observability/ops/healthhttp/history_test.go#L321)

[批次验证与基准](../review-a8c2f28a/README.md)
