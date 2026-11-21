# Card 1930 — 核对低正速率下精确的饱和 RetryAfter 与 Reset

Milestone: 稳定模块质量复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: security
Owned Files: `security/abuse/finite_regression_test.go`
Depends On: 无

Goal: 在已耗尽桶中，两字段以同一基准计量，饱和值等于最大时长而不是回绕或变为零。
Scope: 用合成时钟和成对极小正速率分别产生可表示时长及饱和时长，比较公开 Decision 的 RetryAfter 和 Reset。
Non-goals: 维持稳定公开签名与标准库依赖，将验证限于本卡范围。
Files: `security/abuse/finite_regression_test.go`。
Tests: 在已耗尽桶中，两字段以同一基准计量，饱和值等于最大时长而不是回绕或变为零。
Docs Sync: 记录核验结论；只在现有契约文字需要澄清时另提文档变更。
Done Definition: 完成下列验收，记录验证命令、状态及剩余限制。
Outcome: 合成时钟下，超大等待值饱和到最大 Duration，普通 0.25/s 等待 4 秒，两种 Reset 使用同一时基。

## 证据与影响

第 9 节检查号为 14,22,35,40。证据状态为补证。

已有测试覆盖有限速率和时间不倒序；尚需精确断言等待时间饱和点与正常截断边界。 [security/abuse/limiter.go:602](../../../security/abuse/limiter.go#L602)

[复审报告](../../../docs/evidence/stable-quality-review-2b94cf86.md)

## 验收与验证

- [x] 在已耗尽桶中，两字段以同一基准计量，饱和值等于最大时长而不是回绕或变为零。
- [x] 记录挂载入口、具体断言和结果。已有测试能覆盖时复用并注明。

```sh
go test -race -timeout 60s ./security/abuse
```

## 实施证据

- [TestLimiterRetryAfterSaturatesWithSameResetBase](../../../security/abuse/finite_regression_test.go#L63)

[批次验证与基准](../review-2b94cf86/README.md)
