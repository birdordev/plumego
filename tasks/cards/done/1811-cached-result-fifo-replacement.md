# Card 1811 — 核对 FIFO 缓存替换现有键后的逐出顺序

Milestone: 稳定模块质量复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: router
Owned Files: `router/cache_coverage_test.go`
Depends On: 无

Goal: 更新值可读，FIFO 次序稳定，旧引用可由在途读者安全观察。
Scope: 用容量 2 缓存分别走替换、逐出和清空后写入。
Non-goals: 维持稳定公开签名与标准库依赖，将验证限于本卡范围。
Files: `router/cache_coverage_test.go`。
Tests: 更新值可读，FIFO 次序稳定，旧引用可由在途读者安全观察。
Docs Sync: 复核本卡涉及的已公开约定。
Done Definition: 完成下列验收，记录验证命令、状态及剩余限制。
Outcome: 替换缓存值保持 FIFO 序位。

## 证据与影响

第 9 节检查号为 29,31,32,37。证据状态为补证。

Set 替换命中值不重排 ring，冷分派和容量逐出各有测试。 [router/cache.go:47](../../../router/cache.go#L47)

[复审报告](../../../docs/evidence/stable-quality-review-a074eadb.md)

## 验收与验证

- [x] 更新值可读，FIFO 次序稳定，旧引用可由在途读者安全观察。
- [x] 记录运行结果。若已有用例覆盖目标，记录用例及相关断言。

```sh
go test -race -timeout 60s ./router
```

## 实施证据

- [TestReplacingCacheEntryDoesNotPromoteItsFIFOPosition](../../../router/cache_coverage_test.go#L131)

[批次验证与基准](../review-a074eadb/README.md)
