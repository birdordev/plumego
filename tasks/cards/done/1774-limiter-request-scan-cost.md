# Card 1774 — 测量注入时钟同步扫描对请求成本的影响

Milestone: 稳定模块质量复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: security
Owned Files: `security/abuse/limiter_test.go`
Depends On: 无

Goal: 报告样本量、耗时、分配和原准入决策。
Scope: 测触发扫描和避开扫描两个路径，使用确定性样本及普通桶键。
Non-goals: 维持稳定公开签名与标准库依赖，将验证限于本卡范围。
Files: `security/abuse/limiter_test.go`。
Tests: 报告样本量、耗时、分配和原准入决策。
Docs Sync: 复核本卡涉及的已公开约定。
Done Definition: 完成下列验收，记录验证命令、状态及剩余限制。
Outcome: 以 2048 桶测量注入时钟的同步扫描和避开扫描，验证两种路径的准入结果，记录耗时和分配。

## 证据与影响

第 9 节检查号为 29,32,35,40,48。证据状态为补证。

注入时钟方案把到期扫描转到 Allow，已有活性和桶数回归，未量化大桶集合的请求时延。 [security/abuse/limiter.go:470](../../../security/abuse/limiter.go#L470)

[复审报告](../../../docs/evidence/stable-quality-review-d224acbc.md)

## 验收与验证

- [x] 报告样本量、耗时、分配和原准入决策。
- [x] 记录运行结果。若已有用例覆盖目标，记录用例及相关断言。

```sh
go test -race -timeout 60s ./security/abuse
go test ./security/abuse -run '^$' -bench '^BenchmarkLimiterRequestCleanupCost$' -benchmem -benchtime=100ms -count=3
```

## 实施证据

- [BenchmarkLimiterRequestCleanupCost](../../../security/abuse/limiter_test.go#L940)
- [TestAcceptanceCustomClockCleanupAndStop](../../../security/abuse/limiter_test.go#L868)

[批次验证与基准](../review-d224acbc/README.md)
