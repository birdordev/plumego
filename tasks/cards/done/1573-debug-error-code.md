# Card 1573 — 调试包装派生另一套错误码

Milestone: 本轮质量审查
Recipe: specs/change-recipes/fix-bug.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: middleware
Owned Files: `middleware/debug/debug_errors.go`, `middleware/debug/debug_errors_test.go`, `docs/modules/middleware/README.md`, `website/src/generated/modules.ts`
Depends On: 1572（共享 debug 文件）

Goal: 调试包装派生另一套错误码。
Scope: 先盘点调试消费者的 code 断言与兼容承诺，按授权复用 ErrorType 元数据并同步示例。
Non-goals: 保留现有公开签名和依赖。涉及未授权的行为或兼容契约调整时，先完成调用方核对与兼容决策。
Files: `middleware/debug/debug_errors.go`, `middleware/debug/debug_errors_test.go`, `docs/modules/middleware/README.md`, `website/src/generated/modules.ts`。
Tests: 以 404 和 422 对比标准和调试包装的 code、type、status，明确旧 code 的迁移决定。
Docs Sync: 核对 `docs/modules/middleware/README.md`。 修改模块说明后检查网站生成差异，并在同一交付中保留相应生成文件。
Done Definition: 完成下列验收和验证，记录证据与剩余风险。
Outcome: 盘点发现旧码断言仅在 debug 测试内。改用对应 contract 类型的标准机器码，更新了示例和断言。

## 证据与影响

- 检查号：6,12,21,38。证据状态：源码确认。
- 404 的 StatusText 派生 not_found，而 contract.TypeNotFound 使用 resource_not_found。调试响应因此和标准错误响应产生同一类型、不同 code。 [middleware/debug/debug_errors.go:365](../../../middleware/debug/debug_errors.go#L365)
- [审查报告](../../../docs/evidence/stable-quality-review-dee7aa0f.md)

## 验收与验证

- [x] 以 404 和 422 对比标准和调试包装的 code、type、status，明确旧 code 的迁移决定。
- [x] 运行以下命令并记录结果、失败路径和未验证范围。修改 Go 文件前完成仓库预检，验证前先执行仓库格式化。

```sh
go test -race -timeout 60s ./middleware/debug
make website-sync
make website-check-generated
```

## 实施证据

[TestDebugErrorCodesMatchContract](../../../middleware/debug/debug_errors_test.go#L1177) · [TestDebugReplacementPreservesStatusAndProtocolHeaders](../../../middleware/debug/debug_errors_test.go#L753)
