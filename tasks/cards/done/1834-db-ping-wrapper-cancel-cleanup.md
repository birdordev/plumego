# Card 1834 — 核对池 Ping 在取消后的错误与资源归属

Milestone: 稳定模块质量复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: store
Owned Files: `store/db/sql_test.go`
Depends On: 无

Goal: 保留标准原因，池可再次调用。
Scope: 用受控连接器令首调用取消，随后用正常 Context 重试。
Non-goals: 维持稳定公开签名与标准库依赖，将验证限于本卡范围。
Files: `store/db/sql_test.go`。
Tests: 保留标准原因，池可再次调用。
Docs Sync: 复核本卡涉及的已公开约定。
Done Definition: 完成下列验收，记录验证命令、状态及剩余限制。
Outcome: 取消保留包装分类和标准 Context 原因，原池仍可 Ping。

## 证据与影响

第 9 节检查号为 22,26,29,41。证据状态为补证。

Ping 有错误分类和显式 Context。待核对失败后不关闭调用者拥有的池。 [store/db/sql.go:412](../../../store/db/sql.go#L412)

[复审报告](../../../docs/evidence/stable-quality-review-a074eadb.md)

## 验收与验证

- [x] 保留标准原因，池可再次调用。
- [x] 记录运行结果。若已有用例覆盖目标，记录用例及相关断言。

```sh
go test -race -timeout 60s ./store/db
```

## 实施证据

- [TestCanceledPingLeavesCallerPoolUsable](../../../store/db/sql_test.go#L1477)

[批次验证与基准](../review-a074eadb/README.md)
