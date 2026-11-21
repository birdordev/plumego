# Card 1935 — 核对无定期清理情况下过期项的内存配额

Milestone: 稳定模块质量复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: store
Owned Files: `store/cache/state_regression_test.go`
Depends On: 无

Goal: 容量拒绝和过期回收可区分，回收后重试不影响仍存活条目。
Scope: 合成时钟让一条记录过期并保持周期清理未触发，在另一个键写入前后读取 Stats。
Non-goals: 维持稳定公开签名与标准库依赖，将验证限于本卡范围。
Files: `store/cache/state_regression_test.go`。
Tests: 容量拒绝和过期回收可区分，回收后重试不影响仍存活条目。
Docs Sync: 记录核验结论；只在现有契约文字需要澄清时另提文档变更。
Done Definition: 完成下列验收，记录验证命令、状态及剩余限制。
Outcome: 定期清理未触发时，过期未回收的键仍计容量。读取回收后其他键可重试写入，存活条目保持。

## 证据与影响

第 9 节检查号为 14,28,31,35,41。证据状态为补证。

MaxMemoryUsage 限制追踪载荷，过期但尚未清理的别项仍可能计入。需在清理前、显式读取过期项后分别操作。 [store/cache/memory.go:159](../../../store/cache/memory.go#L159)

[复审报告](../../../docs/evidence/stable-quality-review-2b94cf86.md)

## 验收与验证

- [x] 容量拒绝和过期回收可区分，回收后重试不影响仍存活条目。
- [x] 记录挂载入口、具体断言和结果。已有测试能覆盖时复用并注明。

```sh
go test -race -timeout 60s ./store/cache
```

## 实施证据

- [TestExpiredUncollectedKeyCountsUntilReadCleanup](../../../store/cache/state_regression_test.go#L498)

[批次验证与基准](../review-2b94cf86/README.md)
