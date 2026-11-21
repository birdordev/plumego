# Card 1783 — 核对 QueryRowContext 延迟返回的错误分类

Milestone: 稳定模块质量复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: store
Owned Files: `store/db/sql_test.go`
Depends On: 无

Goal: 保留 sql.ErrNoRows 或原 cause，包装方式一致。
Scope: 复用可控错误的 sql driver 和 ScanRow，核对无行和取消。
Non-goals: 维持稳定公开签名与标准库依赖，将验证限于本卡范围。
Files: `store/db/sql_test.go`。
Tests: 保留 sql.ErrNoRows 或原 cause，包装方式一致。
Docs Sync: 复核本卡涉及的已公开约定。
Done Definition: 完成下列验收，记录验证命令、状态及剩余限制。
Outcome: QueryRowContext 返回延迟行，取消在 Scan 时保留底层原因。无行及普通扫描错误复用已有用例。

## 证据与影响

第 9 节检查号为 6,22,26,41。证据状态为补证。

sql.Row 可能到 Scan 才返回错误，函数无法在包装时获得所有错误。 [store/db/sql.go:246](../../../store/db/sql.go#L246)

[复审报告](../../../docs/evidence/stable-quality-review-d224acbc.md)

## 验收与验证

- [x] 保留 sql.ErrNoRows 或原 cause，包装方式一致。
- [x] 记录运行结果。若已有用例覆盖目标，记录用例及相关断言。

```sh
go test -race -timeout 60s ./store/db
```

## 实施证据

- [TestQueryRowContextClassifiesDeferredCancellationAtScan](../../../store/db/sql_test.go#L1462)
- [TestScanRowNoRows](../../../store/db/sql_test.go#L1137)
- [TestScanRowNonNoRowsError](../../../store/db/sql_test.go#L1208)

[批次验证与基准](../review-d224acbc/README.md)
