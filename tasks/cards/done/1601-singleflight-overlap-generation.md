# Card 1601 — 补证同键下一代请求与旧等待者的隔离

Milestone: 本轮质量审查
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: middleware
Owned Files: `middleware/singleflight/response_regression_test.go`
Depends On: 1579（共享合并器上下文）

Goal: 补证同键下一代请求与旧等待者的隔离。
Scope: 用通道为两个代际标记响应并控制跨代等待者。
Non-goals: 保留现有公开签名、依赖与模块边界。测试若证实新的缺陷，先记录证据再单独安排修复。
Files: `middleware/singleflight/response_regression_test.go`。
Tests: 旧等待者只收到旧响应，新 leader 及其等待者只收到新响应。
Docs Sync: 本卡按现有契约验证。若取证表明契约需调整，先更新后续卡范围。
Done Definition: 完成下列验收和验证，记录证据与剩余风险。
Outcome: 旧等待者阻塞写入时，下一代同键请求完成独立响应，旧响应保持自身代际。

## 证据与影响

- 检查号：28,31,32,39,47。证据状态：补证。
- 结束时先删除 map 项再关闭旧 done；允许下一代 leader 建立独立项。 [middleware/singleflight/singleflight.go:360](../../../middleware/singleflight/singleflight.go#L360)
- [审查报告](../../../docs/evidence/stable-quality-review-dee7aa0f.md)

## 验收与验证

- [x] 旧等待者只收到旧响应，新 leader 及其等待者只收到新响应。
- [x] 运行以下命令并记录结果、失败路径和未验证范围。修改 Go 文件前完成仓库预检，验证前先执行仓库格式化。

```sh
go test -race -timeout 60s ./middleware/singleflight
```

## 实施证据

[TestConcurrentLeaderGenerationsKeepWaiterResponse](../../../middleware/singleflight/response_regression_test.go#L720)
