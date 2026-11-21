# Card 1581 — 静态文件说明忽略已存在的处理分支

Milestone: 本轮质量审查
Recipe: specs/change-recipes/fix-bug.yaml
Context Package: implementation
Priority: P3
State: done
Primary Module: router
Owned Files: `router/static.go`
Depends On: 无

Goal: 静态文件说明忽略已存在的处理分支。
Scope: 在函数注释写明实际三种归一化操作。
Non-goals: 保留现有公开签名和依赖。涉及未授权的行为或兼容契约调整时，先完成调用方核对与兼容决策。
Files: `router/static.go`。
Tests: 复核 Static 和 StaticFS 的根前缀与子前缀测试，使用既有行为。
Docs Sync: 本卡按现有契约验证。若取证表明契约需调整，先更新后续卡范围。
Done Definition: 完成下列验收和验证，记录证据与剩余风险。
Outcome: 纠正归一化注释，核对根前缀、尾斜线和现有分派行为。

## 证据与影响

- 检查号：9,16,24。证据状态：源码确认。
- 注释只说补前导斜线，函数还处理空白、尾斜线及根路径。读注册规则时容易漏掉后两项。 [router/static.go:18](../../../router/static.go#L18)
- [审查报告](../../../docs/evidence/stable-quality-review-dee7aa0f.md)

## 验收与验证

- [x] 复核 Static 和 StaticFS 的根前缀与子前缀测试，使用既有行为。
- [x] 运行以下命令并记录结果、失败路径和未验证范围。修改 Go 文件前完成仓库预检，验证前先执行仓库格式化。

```sh
go test -race -timeout 60s ./router
```

## 实施证据

[TestNormalizeStaticPrefix](../../../router/static_test.go#L103) · [TestStaticRootPrefixRegistersSingleSlashWildcard](../../../router/static_test.go#L81)
