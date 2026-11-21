# Card 1660 — 验证正文包装遇读取错误和 Close 的所有权

Milestone: 稳定模块质量复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: middleware
Owned Files: `middleware/bodylimit/body_limit_test.go`
Depends On: 无

Goal: 验证正文包装遇读取错误和 Close 的所有权。
Scope: 使用可计数的 ReadCloser 注入非容量读失败及 Close 反馈。
Non-goals: 维持公开签名和依赖；涉及现有兼容契约的调整须先核对调用边界及迁移影响。
Files: `middleware/bodylimit/body_limit_test.go`。
Tests: 不会写入假的 413，Close 与主 handler 的契约明确。
Docs Sync: 对照原契约核验，若证据要求修订公开行为，先更新实施范围。
Done Definition: 完成测试与验收记录，并标明剩余取证范围。
Outcome: 基础读失败和关闭错误从包装器向下游保留，不生成 413，底层 Close 只调用一次。

## 证据

检查号为 20,26,29,39,46，状态为补证。

包装器转发 Close、保留底层读错误，超额错误则通过 pending 提交。 [middleware/bodylimit/body_limit.go:214](../../../middleware/bodylimit/body_limit.go#L214)

[复审报告](../../../docs/evidence/stable-quality-review-09a7f5d3.md)

## 验收与验证

- [x] 不会写入假的 413，Close 与主 handler 的契约明确。
- [x] 记录验证命令、状态和剩余范围。若使用既有用例满足验收，记录对应证据。

```sh
go test -race -timeout 60s ./middleware/bodylimit
```

## 实施证据

[TestBodyLimitPreservesUnderlyingErrorsAndClose](../../../middleware/bodylimit/body_limit_test.go#L702)
