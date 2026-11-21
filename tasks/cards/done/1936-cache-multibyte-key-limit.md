# Card 1936 — 核对缓存键限制按字节与错误优先级

Milestone: 稳定模块质量复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P3
State: done
Primary Module: store
Owned Files: `store/cache/state_regression_test.go`
Depends On: 无

Goal: 合法边界能往返，超界不修改缓存且错误分类一致。
Scope: 用正常合成 Unicode 夹具触发等于和超过 MaxKeyLength 两组，记录错误链与统计。
Non-goals: 维持稳定公开签名与标准库依赖，将验证限于本卡范围。
Files: `store/cache/state_regression_test.go`。
Tests: 合法边界能往返，超界不修改缓存且错误分类一致。
Docs Sync: 记录核验结论；只在现有契约文字需要澄清时另提文档变更。
Done Definition: 完成下列验收，记录验证命令、状态及剩余限制。
Outcome: Cache 的多字节键按字节限制，Get、Set、Exists 统一分类，超界不改变统计。

## 证据与影响

第 9 节检查号为 6,14,18,22,41。证据状态为补证。

缓存的长度检查在控制符检查以前。已有普通非法键，需多字节边界及 Get、Set、Exists 使用同一策略。 [store/cache/memory.go:139](../../../store/cache/memory.go#L139)

[复审报告](../../../docs/evidence/stable-quality-review-2b94cf86.md)

## 验收与验证

- [x] 合法边界能往返，超界不修改缓存且错误分类一致。
- [x] 记录挂载入口、具体断言和结果。已有测试能覆盖时复用并注明。

```sh
go test -race -timeout 60s ./store/cache
```

## 实施证据

- [TestCacheAppliesByteKeyLimitAcrossOperations](../../../store/cache/state_regression_test.go#L533)

[批次验证与基准](../review-2b94cf86/README.md)
