# Card 1582 — 门面示例忽略路由注册错误

Milestone: 本轮质量审查
Recipe: specs/change-recipes/fix-bug.yaml
Context Package: implementation
Priority: P3
State: done
Primary Module: .
Owned Files: `plumego.go`, `plumego_test.go`, `docs/modules/plumego/README.md`
Depends On: 无

Goal: 门面示例忽略路由注册错误。
Scope: 示例先检查注册错误，再启动服务器，补核对 Param 示例。
Non-goals: 保留现有公开签名和依赖。涉及未授权的行为或兼容契约调整时，先完成调用方核对与兼容决策。
Files: `plumego.go`, `plumego_test.go`, `docs/modules/plumego/README.md`。
Tests: 运行根包测试和文档片段检查，确认示例仍用批准的六个导出符号。
Docs Sync: 核对 `docs/modules/plumego/README.md`。
Done Definition: 完成下列验收和验证，记录证据与剩余风险。
Outcome: 门面 GoDoc 和模块说明检查注册错误后才启动；根包导出范围保持原有六项。

## 证据与影响

- 检查号：3,16,19,52。证据状态：源码确认。
- GoDoc 直接调用 Get 后开始监听，未展示错误检查。新调用者照抄时可能在注册失败后继续启动。 [plumego.go:7](../../../plumego.go#L7)
- [审查报告](../../../docs/evidence/stable-quality-review-dee7aa0f.md)

## 验收与验证

- [x] 运行根包测试和文档片段检查，确认示例仍用批准的六个导出符号。
- [x] 运行以下命令并记录结果、失败路径和未验证范围。修改 Go 文件前完成仓库预检，验证前先执行仓库格式化。

```sh
go test -race -timeout 60s .
make doc-check
```

## 实施证据

[TestNewReturnsWorkingApp](../../../plumego_test.go#L15)
