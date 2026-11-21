# Card 1690 — 未知健康状态在摘要入口映射为成功

Milestone: 稳定模块质量复审
Recipe: specs/change-recipes/fix-bug.yaml
Context Package: implementation
Priority: P1
State: done
Primary Module: x/observability
Owned Files: `x/observability/ops/healthhttp/helpers.go`, `x/observability/ops/healthhttp/handlers_test.go`, `docs/modules/x/observability/ops/README.md`
Depends On: 无

Goal:

未知健康状态在摘要入口映射为成功。

Scope:

让状态映射显式列出健康和降级，未知或空状态走 503。保留原健康模型和成功响应结构。

Non-goals:

本卡范围限于所列文件。维持公开签名和现有依赖，兼容契约调整先核对调用边界。

Files:

- `x/observability/ops/healthhttp/helpers.go`
- `x/observability/ops/healthhttp/handlers_test.go`
- `docs/modules/x/observability/ops/README.md`

## Impact and Ownership

主模块为 `x/observability`。根模块继续使用标准库。拟编辑范围与运行影响见本卡证据及验收。涉及安全与生命周期的变化按公开入口核验。

## 证据

检查号为 6,21,38,42，状态为已复现。

公开 Manager 返回未知健康状态时，摘要输出 200，而同一模型经 readiness 返回 503。外部监控只消费摘要状态码时会得到成功信号。 [x/observability/ops/healthhttp/helpers.go:66](https://github.com/spcent/plumego/blob/9430574179dc6b793524992833ce27496a5fe548/x/observability/ops/healthhttp/helpers.go#L66)

[复审报告](../../../docs/evidence/stable-quality-review-94305741.md)

## Acceptance Tests

- `x/observability/ops/healthhttp/handlers_test.go` 中的 `TestAcceptanceHealthStateHTTPMapping`

用同一 Manager 对照未知、空、健康、降级和失败状态，检查摘要和 readiness 的约定。

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

- [x] 用同一 Manager 对照未知、空、健康、降级和失败状态，检查摘要和 readiness 的约定。
- [x] 执行验证命令，记录状态、首次失败和剩余核验范围。

Outcome:

健康与降级保留 200 与 206。未知、空和失败状态在摘要和详细探测返回 503，就绪端点对照通过。

## 实施证据

- [TestAcceptanceHealthStateHTTPMapping](../../../x/observability/ops/healthhttp/handlers_test.go#L511)

[批次验证与基准](../review-94305741/README.md)
