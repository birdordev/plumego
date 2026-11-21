# Card 1766 — 核对重复使用同一并发限制 builder 的配额范围

Milestone: 稳定模块质量复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: middleware
Owned Files: `middleware/concurrencylimit/concurrency_limit_test.go`
Depends On: 无

Goal: 共享与隔离的实际范围清楚，取消后 permit 回归。
Scope: 用两个独立 handler 安装同一 builder，与分别构造的版本对照。
Non-goals: 维持稳定公开签名与标准库依赖，将验证限于本卡范围。
Files: `middleware/concurrencylimit/concurrency_limit_test.go`。
Tests: 共享与隔离的实际范围清楚，取消后 permit 回归。
Docs Sync: 复核本卡涉及的已公开约定。
Done Definition: 完成下列验收，记录验证命令、状态及剩余限制。
Outcome: 同一并发限制 builder 的两套 handler 共享许可，独立构造不互相占用。释放后共享入口可继续。

## 证据与影响

第 9 节检查号为 5,14,29,39。证据状态为补证。

并发信号量在 builder 外层创建，重复安装共享资源。该行为是否被清楚记录需通过两个处理器核对。 [middleware/concurrencylimit/concurrency_limit.go:54](../../../middleware/concurrencylimit/concurrency_limit.go#L54)

[复审报告](../../../docs/evidence/stable-quality-review-d224acbc.md)

## 验收与验证

- [x] 共享与隔离的实际范围清楚，取消后 permit 回归。
- [x] 记录运行结果。若已有用例覆盖目标，记录用例及相关断言。

```sh
go test -race -timeout 60s ./middleware/concurrencylimit
```

## 实施证据

- [TestReuseOfOneBuilderSharesAdmissionAcrossHandlers](../../../middleware/concurrencylimit/concurrency_limit_test.go#L433)
- [TestMiddlewareQueuedRequestReturnsOnContextCancel](../../../middleware/concurrencylimit/concurrency_limit_test.go#L156)

[批次验证与基准](../review-d224acbc/README.md)
