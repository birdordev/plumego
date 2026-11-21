# Card 1971 — 核对限流 body Close 时底层错误一次交付

Milestone: 稳定模块质量复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: middleware/bodylimit
Owned Files: `middleware/bodylimit/body_limit_test.go`
Depends On: 无

Goal: Close 错误由调用方处理，底层关闭次数和响应归属可验证。
Scope: 构造可计数 ReadCloser，从实际中间件读取边界后关闭。
Non-goals: 维持稳定公开签名与标准库依赖，将验证限于本卡范围。
Files: `middleware/bodylimit/body_limit_test.go`。
Tests: Close 错误由调用方处理，底层关闭次数和响应归属可验证。
Docs Sync: 记录核验结论；只在现有契约文字需要澄清时另提文档变更。
Done Definition: 完成下列验收，记录验证命令、状态及剩余限制。
Outcome: 读取至精确上限后显式关闭，透传底层 Close 错误，只关闭一次并保持 handler 的成功响应。

## 证据与影响

第 9 节检查号为 15,24,28,39，当前证据状态为补证。 [质量规则第 9 节](../../../docs/operations/agent-code-quality-rules.md#L267)

包装器关闭直接交给底层。读取到边界后的显式 Close 是否仍透传原错误需单独固定。 [middleware/bodylimit/body_limit.go:215](../../../middleware/bodylimit/body_limit.go#L215)

[复审报告](../../../docs/evidence/stable-quality-review-999d5e4d.md)

## 验收与验证

- [x] Close 错误由调用方处理，底层关闭次数和响应归属可验证。
- [x] 记录挂载入口、具体断言和结果。已有测试能覆盖时复用并注明。

```sh
go test -race -timeout 60s ./middleware/bodylimit
```

## 实施证据

- [TestExactLimitDelegatesExplicitBodyClose](../../../middleware/bodylimit/body_limit_test.go#L884)
