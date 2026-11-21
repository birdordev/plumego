# Card 1677 — 验证扫描回调异常后结果集关闭

Milestone: 稳定模块质量复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: store
Owned Files: `store/db/transaction_regression_test.go`
Depends On: 无

Goal: 验证扫描回调异常后结果集关闭。
Scope: 复用受控 rows 驱动，捕捉 panic 和独立 goroutine 的非常规退出。
Non-goals: 维持公开签名和依赖；涉及现有兼容契约的调整须先核对调用边界及迁移影响。
Files: `store/db/transaction_regression_test.go`。
Tests: 底层关闭一次，数据库连接池释放，原异常语义保留。
Docs Sync: 对照原契约核验，若证据要求修订公开行为，先更新实施范围。
Done Definition: 完成测试与验收记录，并标明剩余取证范围。
Outcome: ScanRows、QueryRowStrict 在回调 panic 或 Goexit 后均关闭一次底层行集和释放连接。

## 证据

检查号为 22,29,30,41，状态为补证。

ScanRows 和 QueryRowStrict 都有 defer 关闭。已有事务 Goexit 测试，这两条扫描入口资源仍需独立核对。 [store/db/sql.go:388](../../../store/db/sql.go#L388)

[复审报告](../../../docs/evidence/stable-quality-review-09a7f5d3.md)

## 验收与验证

- [x] 底层关闭一次，数据库连接池释放，原异常语义保留。
- [x] 记录验证命令、状态和剩余范围。若使用既有用例满足验收，记录对应证据。

```sh
go test -race -timeout 60s ./store/db
```

## 实施证据

[TestRowsCallbacksCloseOnPanicAndGoexit](../../../store/db/transaction_regression_test.go#L133)
