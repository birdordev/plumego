# Card 1696 — 关停诊断回调可覆盖 Shutdown 错误

Milestone: 稳定模块质量复审
Recipe: specs/change-recipes/fix-bug.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: core
Owned Files: `core/lifecycle.go`, `core/lifecycle_test.go`, `docs/modules/core/README.md`
Depends On: 无

Goal:

关停诊断回调可覆盖 Shutdown 错误。

Scope:

将关停诊断改为局部 best-effort，保留 Shutdown 原 cause；复核是否采用相同的无路由诊断约定。

Non-goals:

本卡范围限于所列文件。维持公开签名和现有依赖，兼容契约调整先核对调用边界。

Files:

- `core/lifecycle.go`
- `core/lifecycle_test.go`
- `docs/modules/core/README.md`

## Impact and Ownership

主模块为 `core`。根模块继续使用标准库。拟编辑范围与运行影响见本卡证据及验收。涉及安全与生命周期的变化按公开入口核验。

## 证据

检查号为 22,28,33,36，状态为源码确认。

网络关停返回错误时，先调用注入 logger 再包装错误。logger 抛 panic 会掩盖本应可分类的取消或期限错误。 [core/lifecycle.go:95](https://github.com/spcent/plumego/blob/9430574179dc6b793524992833ce27496a5fe548/core/lifecycle.go#L95)

[复审报告](../../../docs/evidence/stable-quality-review-94305741.md)

## Acceptance Tests

- `core/lifecycle_test.go` 中的 `TestAcceptanceShutdownPreservesErrorWhenLoggerPanics`
- `core/lifecycle_test.go` 中的 `TestPostShutdownContractKeepsPreparedAppState`

受控活动连接触发期限并配置人工失败的 logger，断言 errors.Is 和后续关停可用。

Tests:

通过所列模块命令运行相邻用例并核对现有约定。

Docs Sync:

复核 `docs/modules/core/README.md`，模块说明改动后核对生成文件差异。

## Validation

```sh
go test -race -timeout 60s ./core
make website-sync
make website-check-generated
```

Done Definition:

- [x] 受控活动连接触发期限并配置人工失败的 logger，断言 errors.Is 和后续关停可用。
- [x] 执行验证命令，记录状态、首次失败和剩余核验范围。

Outcome:

隔离关停错误诊断中的 panic。活动连接存在时，截止时间错误仍可通过 errors.Is 识别，Serve 正常结束。

## 实施证据

- [TestAcceptanceShutdownPreservesErrorWhenLoggerPanics](../../../core/lifecycle_test.go#L1045)
- [TestPostShutdownContractKeepsPreparedAppState](../../../core/lifecycle_test.go#L148)

[批次验证与基准](../review-94305741/README.md)
