# Card 1579 — 请求合并器没有活跃键数量预算

Milestone: 本轮质量审查
Recipe: specs/change-recipes/fix-bug.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: middleware
Owned Files: `middleware/singleflight/singleflight.go`, `middleware/singleflight/singleflight_test.go`, `docs/modules/middleware/README.md`, `website/src/generated/modules.ts`
Depends On: 无

Goal: 请求合并器没有活跃键数量预算。
Scope: 明确外层并发限制的装配要求，量化并发键成本；只有获得 API 授权后才考虑独立预算。
Non-goals: 保留现有公开签名和依赖。涉及未授权的行为或兼容契约调整时，先完成调用方核对与兼容决策。
Files: `middleware/singleflight/singleflight.go`, `middleware/singleflight/singleflight_test.go`, `docs/modules/middleware/README.md`, `website/src/generated/modules.ts`。
Tests: 用通道保持多个不同键活跃，验证总量及释放时机，记录推荐的已有装配路径。
Docs Sync: 核对 `docs/modules/middleware/README.md`。 修改模块说明后检查网站生成差异，并在同一交付中保留相应生成文件。
Done Definition: 完成下列验收和验证，记录证据与剩余风险。
Outcome: 文档明确活跃键数由 leader 生命周期决定，使用现有外层 concurrencylimit 控制总请求预算。三键并行测试验证结束后全部释放。

## 证据与影响

- 检查号：14,29,35,39,48。证据状态：源码确认。
- 每个活跃键保留请求记录直到 leader 结束，等待者的超时不结束 leader，响应体上限也不控制键总数。慢 handler 下容量取决于外层并发控制。 [middleware/singleflight/singleflight.go:209](../../../middleware/singleflight/singleflight.go#L209)
- [审查报告](../../../docs/evidence/stable-quality-review-dee7aa0f.md)

## 验收与验证

- [x] 用通道保持多个不同键活跃，验证总量及释放时机，记录推荐的已有装配路径。
- [x] 运行以下命令并记录结果、失败路径和未验证范围。修改 Go 文件前完成仓库预检，验证前先执行仓库格式化。

```sh
go test -race -timeout 60s ./middleware/singleflight
make website-sync
make website-check-generated
```

## 实施证据

[TestDistinctLeaderKeysAreReleasedOnCompletion](../../../middleware/singleflight/response_regression_test.go#L679)
