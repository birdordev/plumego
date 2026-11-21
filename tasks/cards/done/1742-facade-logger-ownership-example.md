# Card 1742 — 核对根门面的依赖别名和默认日志

Milestone: 稳定模块质量复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P3
State: done
Primary Module: .
Owned Files: `plumego_test.go`
Depends On: 无

Goal:

核对根门面的依赖别名和默认日志。

Scope:

从公开入口验证缺省 logger 和别名兼容。

Non-goals:

本卡范围限于所列文件。维持公开签名和现有依赖，兼容契约调整先核对调用边界。

Files:

- `plumego_test.go`

## Impact and Ownership

主模块为 `.`。根模块继续使用标准库。候审目标是复核可观察契约并记录证据。

## 证据

检查号为 3,5,6,16，状态为补证。

根门面构造使用空依赖，别名保留给显式 core 装配。当前示例首要展示最小服务。 [plumego.go:34](https://github.com/spcent/plumego/blob/9430574179dc6b793524992833ce27496a5fe548/plumego.go#L34)

[复审报告](../../../docs/evidence/stable-quality-review-94305741.md)

## Acceptance Tests

- `plumego_test.go` 中的 `TestFacadeConstructorsRetainCoreDependencyContract`
- `plumego_test.go` 中的 `TestNewReturnsWorkingApp`

没有新增门面职责，示例与批准导出集合相符。

Tests:

通过所列模块命令运行相邻用例并核对现有约定。

Docs Sync:

对照原契约核验，若证据要求修订公开行为，先更新实施范围。

## Validation

```sh
go test -race -timeout 60s .
```

Done Definition:

- [x] 没有新增门面职责，示例与批准导出集合相符。
- [x] 执行验证命令，记录状态、首次失败和剩余核验范围。

Outcome:

根门面的两个构造获得 core 的默认 logger。以门面类型直接传入 core 装配，确认别名兼容。

## 实施证据

- [TestFacadeConstructorsRetainCoreDependencyContract](../../../plumego_test.go#L177)
- [TestNewReturnsWorkingApp](../../../plumego_test.go#L16)

[批次验证与基准](../review-94305741/README.md)
