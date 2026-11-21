# Card 1826 — 核对 Stop 等待注入时钟的在途扫描

Milestone: 稳定模块质量复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: security
Owned Files: `security/abuse/limiter_test.go`
Depends On: 无

Goal: 两个 Stop 返回时扫描已结束，此后不会再扫描。
Scope: 以受控锁屏障让扫描处在进行中，两个 Stop 等待，释放后验证准入。
Non-goals: 维持稳定公开签名与标准库依赖，将验证限于本卡范围。
Files: `security/abuse/limiter_test.go`。
Tests: 两个 Stop 返回时扫描已结束，此后不会再扫描。
Docs Sync: 复核本卡涉及的已公开约定。
Done Definition: 完成下列验收，记录验证命令、状态及剩余限制。
Outcome: Stop 等待正在执行的清理且结束后停用任务。

## 证据与影响

第 9 节检查号为 28,29,32,40,47。证据状态为补证。

旧回归覆盖回调内 Stop 与默认后台退出，尚需证明自定义 requestCleanup 正持扫描所有权时并发 Stop 的等待。 [security/abuse/limiter.go:455](../../../security/abuse/limiter.go#L455)

[复审报告](../../../docs/evidence/stable-quality-review-a074eadb.md)

## 验收与验证

- [x] 两个 Stop 返回时扫描已结束，此后不会再扫描。
- [x] 记录运行结果。若已有用例覆盖目标，记录用例及相关断言。

```sh
go test -race -timeout 60s ./security/abuse
```

## 实施证据

- [TestCustomLimiterStopWaitsForAnActiveCleanup](../../../security/abuse/limiter_test.go#L969)

[批次验证与基准](../review-a074eadb/README.md)
