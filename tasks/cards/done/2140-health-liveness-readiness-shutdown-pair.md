# Card 2140 — 核对关闭中的存活与就绪 HTTP 契约

Milestone: 稳定模块第 9 节复审
Recipe: specs/change-recipes/analysis-only.yaml
Context Package: review
Priority: P3
State: done
Primary Module: x/observability/ops
Owned Files: `x/observability/ops/healthhttp/handlers_test.go`
Depends On: 无

Goal: 核对关闭中的存活与就绪 HTTP 契约。
Scope: 用同一宿主先使其就绪，卡住检查后关闭，核对两个处理器的职责和 JSON 状态。
Non-goals: 本卡提交取证结果。后续行为变更由独立修复卡执行。
Files: `x/observability/ops/healthhttp/handlers_test.go`
Tests: 断言存活 200、就绪 503、旧检查不能恢复就绪，使用通道关闭检查。
Docs Sync: 核对既有文档与验收结果一致，记录需要后续同步的位置。
Done Definition: 记录命令、结果、证据限制和是否需要独立修复卡。
Outcome: 对同一管理器阻塞检查、关闭并释放检查后，LiveHandler 返回 200，ReadinessHandler 返回 503。已关闭状态没有被旧检查恢复。

## 证据与影响

证据状态为待补证。第 9 节检查号为 17,21,28,42,45,46。已有独立存活、就绪和关闭测试。使用同一内置 Manager，在并发关闭前后检查两个公开响应的状态与响应体，尚待复验。 [x/observability/ops/healthhttp/handlers.go:143](../../../x/observability/ops/healthhttp/handlers.go#L143) [x/observability/ops/healthhttp/readiness.go:19](../../../x/observability/ops/healthhttp/readiness.go#L19) [x/observability/ops/healthhttp/manager.go:535](../../../x/observability/ops/healthhttp/manager.go#L535)

[复审报告](../../../docs/evidence/stable-quality-review-6e8d53f9.md)

## 验收与验证

- [x] 断言存活 200、就绪 503、旧检查不能恢复就绪，使用通道关闭检查。
- [x] 记录命令、结果、证据限制和是否需要独立修复卡。

```sh
go test -race -timeout 60s ./x/observability/ops/healthhttp
```
