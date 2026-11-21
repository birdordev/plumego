# Card 2049 — ScanRows 中途扫描和关闭失败

Milestone: 稳定模块第 9 节复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: store/db
Owned Files: `store/db/sql_test.go`
Depends On: 无

Goal: ScanRows 中途扫描和关闭失败
Scope: 用现有假驱动在第二行使扫描与 Close 都失败。
Non-goals: 范围限于本卡涉及的契约，保持公开签名和依赖。
Files: `store/db/sql_test.go`。
Tests: 两个错误均可分类，部分结果的形状遵循已有契约，关闭一次。
Docs Sync: 核对新增验收与已有模块说明一致，记录证据和限制。
Done Definition: 完成以下验收，记录命令、结果与剩余限制。
Outcome: 复用两行驱动桩。第二行扫描与关闭错误均保留，关闭一次，无部分结果，可复用连接池。

## 证据与影响

证据状态为补证。第 9 节检查号为 22,29,30,41,46。 [质量规则第 9 节](../../../docs/operations/agent-code-quality-rules.md#L267)

ScanRows 持有 sql.Rows，并在返回时拼接关闭错误。 [store/db/sql.go:385](../../../store/db/sql.go#L385)

[复审报告](../../../docs/evidence/stable-quality-review-c3a77410.md)

## 验收与验证

- [x] 两个错误均可分类，部分结果的形状遵循已有契约，关闭一次。
- [x] 已有测试覆盖相同触发和断言时可直接复用，记录具体用例。

```sh
go test -race -timeout 60s ./store/db
```

## 实施证据

- [TestScanRowsDiscardsPartialResultsAndRetainsCloseCause](../../../store/db/sql_test.go#L1555)
