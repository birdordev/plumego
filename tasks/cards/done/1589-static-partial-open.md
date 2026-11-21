# Card 1589 — 补证自定义静态文件系统部分打开所有权

Milestone: 本轮质量审查
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: router
Owned Files: `router/static_test.go`
Depends On: 无

Goal: 补证自定义静态文件系统部分打开所有权。
Scope: 补统计 Close 次数的受控文件。
Non-goals: 保留现有公开签名、依赖与模块边界。测试若证实新的缺陷，先记录证据再单独安排修复。
Files: `router/static_test.go`。
Tests: 每条拒绝路径返回 404，非 nil 文件恰好关闭一次。
Docs Sync: 本卡按现有契约验证。若取证表明契约需调整，先更新后续卡范围。
Done Definition: 完成下列验收和验证，记录证据与剩余风险。
Outcome: 追加 Stat 失败时关闭一次的回归，与既有部分打开和非普通文件拒绝测试一起验证。

## 证据与影响

- 检查号：24,29,37,46。证据状态：补证。
- StaticFS 防御文件和错误同时返回、Stat 失败以及非普通文件。 [router/static.go:210](../../../router/static.go#L210)
- [审查报告](../../../docs/evidence/stable-quality-review-dee7aa0f.md)

## 验收与验证

- [x] 每条拒绝路径返回 404，非 nil 文件恰好关闭一次。
- [x] 运行以下命令并记录结果、失败路径和未验证范围。修改 Go 文件前完成仓库预检，验证前先执行仓库格式化。

```sh
go test -race -timeout 60s ./router
```

## 实施证据

[TestStaticFSClosesFilesFromInjectedOpen](../../../router/static_test.go#L278) · [TestStaticFSRejectsNonRegularFiles](../../../router/static_test.go#L339)
