# Card 1572 — 调试错误响应默认回显完整查询

Milestone: 本轮质量审查
Recipe: specs/change-recipes/fix-bug.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: middleware
Owned Files: `middleware/debug/debug_errors.go`, `middleware/debug/debug_errors_test.go`, `docs/modules/middleware/README.md`, `website/src/generated/modules.ts`
Depends On: 无

Goal: 调试错误响应默认回显完整查询。
Scope: 默认关闭查询回显。对显式启用的路径仅输出经审核的非敏感元数据，并同步调试配置说明。
Non-goals: 保留现有公开签名和依赖。涉及未授权的行为或兼容契约调整时，先完成调用方核对与兼容决策。
Files: `middleware/debug/debug_errors.go`, `middleware/debug/debug_errors_test.go`, `docs/modules/middleware/README.md`, `website/src/generated/modules.ts`。
Tests: 使用无秘密的测试占位值验证默认及启用路径的查询裁剪，同时保持错误状态与 request_id。
Docs Sync: 核对 `docs/modules/middleware/README.md`。 修改模块说明后检查网站生成差异，并在同一交付中保留相应生成文件。
Done Definition: 完成下列验收和验证，记录证据与剩余风险。
Outcome: 默认省略查询信息；显式开启只报告 query_present，保留 request_id 和 HTTP 状态。

## 证据与影响

- 检查号：21,25,39,46。证据状态：源码确认。
- 默认 IncludeQuery 为 true，符合替换条件的响应直接携带 RawQuery。调试模式下，可能把含机密参数的查询复制进响应及其下游记录。生产使用情况未验证。 [middleware/debug/debug_errors.go:373](../../../middleware/debug/debug_errors.go#L373)
- [审查报告](../../../docs/evidence/stable-quality-review-dee7aa0f.md)

## 验收与验证

- [x] 使用无秘密的测试占位值验证默认及启用路径的查询裁剪，同时保持错误状态与 request_id。
- [x] 运行以下命令并记录结果、失败路径和未验证范围。修改 Go 文件前完成仓库预检，验证前先执行仓库格式化。

```sh
go test -race -timeout 60s ./middleware/debug
make website-sync
make website-check-generated
```

## 实施证据

[TestDebugDefaultsOmitQueryMetadata](../../../middleware/debug/debug_errors_test.go#L1153) · [TestDebugErrorsNotFoundRequestDetails](../../../middleware/debug/debug_errors_test.go#L692)
