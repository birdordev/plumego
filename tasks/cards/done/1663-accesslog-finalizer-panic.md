# Card 1663 — 验证 logger 失败不会改变已提交结果

Milestone: 稳定模块质量复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: middleware
Owned Files: `middleware/accesslog/accesslog_test.go`
Depends On: 无

Goal: 验证 logger 失败不会改变已提交结果。
Scope: 用人工失败的 logger 观察早提交、无显式写和 HEAD 记录。
Non-goals: 维持公开签名和依赖；涉及现有兼容契约的调整须先核对调用边界及迁移影响。
Files: `middleware/accesslog/accesslog_test.go`。
Tests: 响应最终只提交一次，日志异常无外传，panic 策略按既有契约。
Docs Sync: 对照原契约核验，若证据要求修订公开行为，先更新实施范围。
Done Definition: 完成测试与验收记录，并标明剩余取证范围。
Outcome: 日志回调 panic 后，早提交、无显式写及 HEAD 的响应保持原结果。

## 证据

检查号为 23,25,33,39，状态为补证。

访问日志是尾部观察动作，观察失败拟由 shared finalizer 吞掉。 [middleware/accesslog/accesslog.go:48](../../../middleware/accesslog/accesslog.go#L48)

[复审报告](../../../docs/evidence/stable-quality-review-09a7f5d3.md)

## 验收与验证

- [x] 响应最终只提交一次，日志异常无外传，panic 策略按既有契约。
- [x] 记录验证命令、状态和剩余范围。若使用既有用例满足验收，记录对应证据。

```sh
go test -race -timeout 60s ./middleware/accesslog
```

## 实施证据

[TestLoggerPanicLeavesNormalResponsesIntact](../../../middleware/accesslog/accesslog_test.go#L261) · [TestMiddlewarePreservesDownstreamPanicWhenLoggerPanics](../../../middleware/accesslog/accesslog_test.go#L199)
