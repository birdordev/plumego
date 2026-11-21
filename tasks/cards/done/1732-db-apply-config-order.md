# Card 1732 — 补证数据库连接池重配置的顺序和零值

Milestone: 稳定模块质量复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: store
Owned Files: `store/db/sql_test.go`
Depends On: 无

Goal:

补证数据库连接池重配置的顺序和零值。

Scope:

通过自定义 driver 和 DB.Stats 核对小限额、清零及随后再设置。

Non-goals:

本卡范围限于所列文件。维持公开签名和现有依赖，兼容契约调整先核对调用边界。

Files:

- `store/db/sql_test.go`

## Impact and Ownership

主模块为 `store`。根模块继续使用标准库。候审目标是复核可观察契约并记录证据。

## 证据

检查号为 14,29,32,41，状态为补证。

ApplyConfig 将 idle 限制约束在 open 以下并应用零值，数据库统计可见部分配置。 [store/db/sql.go:199](https://github.com/spcent/plumego/blob/9430574179dc6b793524992833ce27496a5fe548/store/db/sql.go#L199)

[复审报告](../../../docs/evidence/stable-quality-review-94305741.md)

## Acceptance Tests

- `store/db/sql_test.go` 中的 `TestApplyConfigMaxOpenConns`
- `store/db/sql_test.go` 中的 `TestApplyConfigResetsPoolLimitsToZero`

配置变化不创建隐式连接、后续操作与约定一致。

Tests:

通过所列模块命令运行相邻用例并核对现有约定。

Docs Sync:

对照原契约核验，若证据要求修订公开行为，先更新实施范围。

## Validation

```sh
go test -race -timeout 60s ./store/db
```

Done Definition:

- [x] 配置变化不创建隐式连接、后续操作与约定一致。
- [x] 执行验证命令，记录状态、首次失败和剩余核验范围。

Outcome:

验证池配置重设为零并缩小后，构造没有隐式建连，显式 Ping 才按新限制填充连接池。

## 实施证据

- [TestApplyConfigMaxOpenConns](../../../store/db/sql_test.go#L65)
- [TestApplyConfigResetsPoolLimitsToZero](../../../store/db/sql_test.go#L78)

[批次验证与基准](../review-94305741/README.md)
