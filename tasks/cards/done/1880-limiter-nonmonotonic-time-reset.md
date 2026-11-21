# Card 1880 — 补证合成时钟回拨后的 RetryAfter 与 Reset

Milestone: 稳定模块质量复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P3
State: done
Primary Module: security
Owned Files: `security/abuse/limiter_test.go`
Depends On: 无

Goal: 余量不会因时钟回拨变负或异常上涨，重试与重置时间关系有清楚记录。
Scope: 用脚本化无网络时钟生成正常、回拨及恢复读数，观察只使用本机键。
Non-goals: 维持稳定公开签名与标准库依赖，将验证限于本卡范围。
Files: `security/abuse/limiter_test.go`。
Tests: 余量不会因时钟回拨变负或异常上涨，重试与重置时间关系有清楚记录。
Docs Sync: 记录核验结论；只在现有契约文字需要澄清时另提文档变更。
Done Definition: 完成下列验收，记录验证命令、状态及剩余限制。
Outcome: 合成时钟回拨不会恢复已经消耗的令牌，Reset 与 RetryAfter 保存同一基准。

## 证据与影响

第 9 节检查号为 14,28,32,40。证据状态为补证。

桶内 refill 时间不倒退，输出 Reset 还会读取第二次时钟。需要固定两次读的顺序与已消耗令牌。 [security/abuse/limiter.go:393](../../../security/abuse/limiter.go#L393)

[复审报告](../../../docs/evidence/stable-quality-review-a8c2f28a.md)

## 验收与验证

- [x] 余量不会因时钟回拨变负或异常上涨，重试与重置时间关系有清楚记录。
- [x] 记录挂载入口、具体断言和结果。已有测试能覆盖时复用并注明。

```sh
go test -race -timeout 60s ./security/abuse
```

## 实施证据

- [TestLimiterResetDoesNotMoveBackwardOnClockRollback](../../../security/abuse/limiter_test.go#L1015)

[批次验证与基准](../review-a8c2f28a/README.md)
