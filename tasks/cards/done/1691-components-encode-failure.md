# Card 1691 — 全量组件端点在编码失败时返回空 200

Milestone: 稳定模块质量复审
Recipe: specs/change-recipes/fix-bug.yaml
Context Package: implementation
Priority: P1
State: done
Primary Module: x/observability
Owned Files: `x/observability/ops/healthhttp/handlers.go`, `x/observability/ops/healthhttp/handlers_test.go`, `docs/modules/x/observability/ops/README.md`, `x/observability/ops/healthhttp/helpers.go`
Depends On: 无

Goal:

全量组件端点在编码失败时返回空 200。

Scope:

复用健康宿主已有的提交前编码与固定错误处理，并验证写失败之后不会尝试第二次提交。

Non-goals:

本卡范围限于所列文件。维持公开签名和现有依赖，兼容契约调整先核对调用边界。

Files:

- `x/observability/ops/healthhttp/helpers.go`
- `x/observability/ops/healthhttp/handlers.go`
- `x/observability/ops/healthhttp/handlers_test.go`
- `docs/modules/x/observability/ops/README.md`

## Impact and Ownership

主模块为 `x/observability`。根模块继续使用标准库。拟编辑范围与运行影响见本卡证据及验收。涉及安全与生命周期的变化按公开入口核验。

## 证据

检查号为 21,22,23,38,42,46，状态为已复现。

上一轮修过 writeHealthResponse 的提交前编码，本端点仍忽略 WriteResponse 的失败。包含一个不可编码的人工详情时录得 200、零正文，调用方无法区分有效探测。 [x/observability/ops/healthhttp/handlers.go:129](https://github.com/spcent/plumego/blob/9430574179dc6b793524992833ce27496a5fe548/x/observability/ops/healthhttp/handlers.go#L129)

[复审报告](../../../docs/evidence/stable-quality-review-94305741.md)

## Acceptance Tests

- `x/observability/ops/healthhttp/handlers_test.go` 中的 `TestAcceptanceAllComponentsEncodingAndWriteFailures`
- `x/observability/ops/healthhttp/handlers_test.go` 中的 `TestAllComponentsHealthHandler`

通过 AllComponentsHealthHandler 传入正常详情及不可编码占位值，核对最终状态、结构、request_id 及提交次数。

Tests:

通过所列模块命令运行相邻用例并核对现有约定。

Docs Sync:

复核 `docs/modules/x/observability/ops/README.md`，模块说明改动后核对生成文件差异。

## Validation

```sh
go test -race -timeout 60s ./x/observability/ops/healthhttp
make website-sync
make website-check-generated
```

Done Definition:

- [x] 通过 AllComponentsHealthHandler 传入正常详情及不可编码占位值，核对最终状态、结构、request_id 及提交次数。
- [x] 执行验证命令，记录状态、首次失败和剩余核验范围。

Outcome:

复用宿主健康文档 writer。普通快照保留 200 和 data，编码错误或 panic 在提交前返回固定的 500 与 request_id，底层提交后失败只尝试一次。

## 实施证据

- [TestAcceptanceAllComponentsEncodingAndWriteFailures](../../../x/observability/ops/healthhttp/handlers_test.go#L543)
- [TestAllComponentsHealthHandler](../../../x/observability/ops/healthhttp/handlers_test.go#L179)

[批次验证与基准](../review-94305741/README.md)
