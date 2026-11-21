# Card 1701 — DB 打开文档缺少连接池延迟建连约定

Milestone: 稳定模块质量复审
Recipe: specs/change-recipes/fix-bug.yaml
Context Package: implementation
Priority: P3
State: done
Primary Module: store
Owned Files: `store/db/sql.go`, `docs/modules/store/README.md`, `store/db/sql_test.go`
Depends On: 无

Goal:

DB 打开文档缺少连接池延迟建连约定。

Scope:

明确返回池句柄，并引用现有 Ping 入口。

Non-goals:

本卡范围限于所列文件。维持公开签名和现有依赖，兼容契约调整先核对调用边界。

Files:

- `store/db/sql_test.go`
- `store/db/sql.go`
- `docs/modules/store/README.md`

## Impact and Ownership

主模块为 `store`。根模块继续使用标准库。拟编辑范围与运行影响见本卡证据及验收。涉及安全与生命周期的变化按公开入口核验。

## 证据

检查号为 6,15,16,41，状态为源码确认。

Open 默认调用 sql.Open 并配置 pool，GoDoc 却称打开数据库连接。调用者可能将构造成功作为网络可达判据。 [store/db/sql.go:144](https://github.com/spcent/plumego/blob/9430574179dc6b793524992833ce27496a5fe548/store/db/sql.go#L144)

[复审报告](../../../docs/evidence/stable-quality-review-94305741.md)

## Acceptance Tests

- `store/db/sql_test.go` 中的 `TestOpenWithDoesNotPingDuringOpen`

用桩驱动计数验证配置过程和显式 Ping 的不同阶段。

Tests:

通过所列模块命令运行相邻用例并核对现有约定。

Docs Sync:

已复核 store 模块文档，其中已有 Open 延迟建连和显式 Ping 的说明。

## Validation

```sh
go test -race -timeout 60s ./store/db
```

Done Definition:

- [x] 用桩驱动计数验证配置过程和显式 Ping 的不同阶段。
- [x] 执行验证命令，记录状态、首次失败和剩余核验范围。

Outcome:

GoDoc 将 Open 描述为构造连接池并指向 Ping，模块说明已具同一约定。桩驱动验证 Open 不创建隐式连接，显式 Ping 才执行探测。

## 实施证据

- [TestOpenWithDoesNotPingDuringOpen](../../../store/db/sql_test.go#L109)

[批次验证与基准](../review-94305741/README.md)
