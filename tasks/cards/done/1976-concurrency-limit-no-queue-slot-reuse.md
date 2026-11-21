# Card 1976 — 核对无排队配置下拒绝后的容量恢复

Milestone: 稳定模块质量复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: middleware/concurrencylimit
Owned Files: `middleware/concurrencylimit/concurrency_limit_test.go`
Depends On: 无

Goal: 并发峰值为一，拒绝不占槽位，余下请求正常通过。
Scope: 用通道令第一个请求占满唯一槽位，再观察拒绝与释放后的成功。
Non-goals: 维持稳定公开签名与标准库依赖，将验证限于本卡范围。
Files: `middleware/concurrencylimit/concurrency_limit_test.go`。
Tests: 并发峰值为一，拒绝不占槽位，余下请求正常通过。
Docs Sync: 记录核验结论；只在现有契约文字需要澄清时另提文档变更。
Done Definition: 完成下列验收，记录验证命令、状态及剩余限制。
Outcome: 无等待队列时的拒绝不消耗残余容量，原请求释放后下一请求立即得到槽位。

## 证据与影响

第 9 节检查号为 26,34,35,39,47，当前证据状态为补证。 [质量规则第 9 节](../../../docs/operations/agent-code-quality-rules.md#L267)

无等待预算时 capacity 分支直接结束。现有 TestMiddlewareFailFastWhenNoQueueDepth 覆盖拒绝和 owner 释放，尚缺同一实例接收第三次请求的断言。 [middleware/concurrencylimit/concurrency_limit.go:105](../../../middleware/concurrencylimit/concurrency_limit.go#L105)

[复审报告](../../../docs/evidence/stable-quality-review-999d5e4d.md)

## 验收与验证

- [x] 并发峰值为一，拒绝不占槽位，余下请求正常通过。
- [x] 记录挂载入口、具体断言和结果。已有测试能覆盖时复用并注明。

```sh
go test -race -timeout 60s ./middleware/concurrencylimit
```

## 实施证据

- [TestNoQueueReleasesSlotsAfterRejectedCall](../../../middleware/concurrencylimit/concurrency_limit_test.go#L500)
