# Card 1586 — 验证并发关停的诊断协程所有权

Milestone: 本轮质量审查
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: core
Owned Files: `core/coverage_test.go`
Depends On: 无

Goal: 验证并发关停的诊断协程所有权。
Scope: 复用受控连接和 logger 屏障。
Non-goals: 保留现有公开签名、依赖与模块边界。测试若证实新的缺陷，先记录证据再单独安排修复。
Files: `core/coverage_test.go`。
Tests: 两个关停上下文独立结束，关闭连接和成功重试后无活动 drain，诊断只由当前尝试发送。
Docs Sync: 本卡按现有契约验证。若取证表明契约需调整，先更新后续卡范围。
Done Definition: 完成下列验收和验证，记录证据与剩余风险。
Outcome: 两次重叠 Shutdown 取得独立返回，第二次不抢占第一次 drain，结束后成功重试。

## 证据与影响

- 检查号：28,29,32,36,47。证据状态：补证。
- 独占 drain 由发起者释放，多个 Shutdown 的先后退出影响诊断会话。 [core/lifecycle.go:146](../../../core/lifecycle.go#L146)
- [审查报告](../../../docs/evidence/stable-quality-review-dee7aa0f.md)

## 验收与验证

- [x] 两个关停上下文独立结束，关闭连接和成功重试后无活动 drain，诊断只由当前尝试发送。
- [x] 运行以下命令并记录结果、失败路径和未验证范围。修改 Go 文件前完成仓库预检，验证前先执行仓库格式化。

```sh
go test -race -timeout 60s ./core
```

## 实施证据

[TestOverlappingShutdownsKeepDrainOwnershipScoped](../../../core/coverage_test.go#L542)
