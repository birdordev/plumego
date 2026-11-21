# Card 1638 — 显式正文预览直接复制应用错误正文

Milestone: 稳定模块质量复审
Recipe: specs/change-recipes/analysis-only.yaml
Context Package: review
Priority: P2
State: done
Primary Module: middleware
Owned Files: `middleware/debug/debug_errors.go`, `middleware/debug/debug_errors_test.go`, `middleware/debug/README.md`, `docs/modules/middleware/README.md`
Depends On: 无

Goal: 显式正文预览直接复制应用错误正文。
Scope: 盘点显式调用后，优先改为只报告大小和是否截断；如需正文保留能力，先确定经过调用者审核的允许字段契约。
Non-goals: 维持公开签名和依赖；涉及现有兼容契约的调整须先核对调用边界及迁移影响。
Files: `middleware/debug/debug_errors.go`, `middleware/debug/debug_errors_test.go`, `middleware/debug/README.md`, `docs/modules/middleware/README.md`。
Tests: 用无敏感数据的占位正文核对默认和启用路径的元数据，保留状态、缓存和 request_id 语义。
Docs Sync: 复核 `docs/modules/middleware/README.md`，模块说明改动后核对生成文件差异。
Done Definition: 完成测试与验收记录，并标明剩余取证范围。
Outcome: IncludeBody 在替换响应时只报告非空捕获正文的字节数。超过捕获上限时沿用原始 pass-through。

## 证据

检查号为 21,25,35,39，状态为源码确认。

IncludeBody 是开发模式的显式开关。当前只按字节截断，应用错误正文中的任意敏感字段依然可能进入重组后的响应。部署启用情况未核实。 [middleware/debug/debug_errors.go:378](../../../middleware/debug/debug_errors.go#L378)

[复审报告](../../../docs/evidence/stable-quality-review-09a7f5d3.md)

## 验收与验证

- [x] 用无敏感数据的占位正文核对默认和启用路径的元数据，保留状态、缓存和 request_id 语义。
- [x] 记录验证命令、状态和剩余范围。若使用既有用例满足验收，记录对应证据。

```sh
go test -race -timeout 60s ./middleware/debug
make website-sync
make website-check-generated
```

## 实施证据

[TestDebugErrorsIncludeBodyReportsSizeWithoutText](../../../middleware/debug/debug_errors_test.go#L338) · [TestDebugErrorsCaptureLimitPassesThroughOriginalResponse](../../../middleware/debug/debug_errors_test.go#L311)
