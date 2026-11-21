# Card 1781 — 核对缓存删除在取消和关闭同时发生时的判决

Milestone: 稳定模块质量复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: store
Owned Files: `store/cache/state_regression_test.go`
Depends On: 无

Goal: 无意外删除，错误可分类，后续关闭完成。
Scope: 阻塞写屏障、取消请求、恢复执行，再检查值和统计。
Non-goals: 维持稳定公开签名与标准库依赖，将验证限于本卡范围。
Files: `store/cache/state_regression_test.go`。
Tests: 无意外删除，错误可分类，后续关闭完成。
Docs Sync: 复核本卡涉及的已公开约定。
Done Definition: 完成下列验收，记录验证命令、状态及剩余限制。
Outcome: 删除通过入口检查并等待写边界时关闭与取消交叠，返回取消，保留最终计数，关闭完成。

## 证据与影响

第 9 节检查号为 22,26,28,41。证据状态为补证。

删除含入口状态检查和写锁后的重查。需在可控制等待下确认取消优先级和计数。 [store/cache/memory.go:268](../../../store/cache/memory.go#L268)

[复审报告](../../../docs/evidence/stable-quality-review-d224acbc.md)

## 验收与验证

- [x] 无意外删除，错误可分类，后续关闭完成。
- [x] 记录运行结果。若已有用例覆盖目标，记录用例及相关断言。

```sh
go test -race -timeout 60s ./store/cache
```

## 实施证据

- [TestDeleteCanceledWhileCloseWaitsRetainsFinalCounts](../../../store/cache/state_regression_test.go#L395)

[批次验证与基准](../review-d224acbc/README.md)
