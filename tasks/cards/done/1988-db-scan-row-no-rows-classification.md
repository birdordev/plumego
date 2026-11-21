# Card 1988 — 核对 ScanRow 无记录和 nil 输入的错误区分

Milestone: 稳定模块质量复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: store/db
Owned Files: `store/db/sql_test.go`
Depends On: 无

Goal: 记录 errors.Is 匹配、语义分类和后继查询结果。
Scope: 复用本机 SQL 桩，分别获取零结果的 row 并传 nil。
Non-goals: 维持稳定公开签名与标准库依赖，将验证限于本卡范围。
Files: `store/db/sql_test.go`。
Tests: 记录 errors.Is 匹配、语义分类和后继查询结果。
Docs Sync: 记录核验结论；只在现有契约文字需要澄清时另提文档变更。
Done Definition: 完成下列验收，记录验证命令、状态及剩余限制。
Outcome: ScanRow 区分无记录与 nil 输入，并保留 errors.Is 链。连接池仍可继续使用。

## 证据与影响

第 9 节检查号为 15,18,21,41，当前证据状态为补证。 [质量规则第 9 节](../../../docs/operations/agent-code-quality-rules.md#L267)

现有 TestScanRowNoRows 已检查包级错误。还需验证其与 nil *Row 的错误分类互斥，两种调用都不影响后继查询。 [store/db/sql.go:367](../../../store/db/sql.go#L367)

[复审报告](../../../docs/evidence/stable-quality-review-999d5e4d.md)

## 验收与验证

- [x] 记录 errors.Is 匹配、语义分类和后继查询结果。
- [x] 记录挂载入口、具体断言和结果。已有测试能覆盖时复用并注明。

```sh
go test -race -timeout 60s ./store/db
```

## 实施证据

- [TestScanRowKeepsMissingRowAndInvalidInputDistinct](../../../store/db/sql_test.go#L1584)
