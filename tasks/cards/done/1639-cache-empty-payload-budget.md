# Card 1639 — 缓存的 payload 配额无法约束空值条目数量

Milestone: 稳定模块质量复审
Recipe: specs/change-recipes/analysis-only.yaml
Context Package: review
Priority: P2
State: done
Primary Module: store
Owned Files: `store/cache/memory.go`, `store/cache/memory_bench_test.go`, `docs/modules/store/README.md`, `website/src/generated/modules.ts`
Depends On: 无

Goal: 缓存的 payload 配额无法约束空值条目数量。
Scope: 先强化容量说明并测量空值与短键的成本；保留已有配额契约，需条目限额时独立评估授权。
Non-goals: 维持公开签名和依赖；涉及现有兼容契约的调整须先核对调用边界及迁移影响。
Files: `store/cache/memory.go`, `store/cache/memory_bench_test.go`, `docs/modules/store/README.md`, `website/src/generated/modules.ts`。
Tests: 固定小 payload 限额比较多条空值、普通覆盖、Clear 后的成本和计数。
Docs Sync: 复核 `docs/modules/store/README.md`，模块说明改动后核对生成文件差异。
Done Definition: 完成测试与验收记录，并标明剩余取证范围。
Outcome: 澄清配额只计 payload。基准记录空值条目创建、覆盖及清空的成本，保持已有容量和配置行为。

## 证据

检查号为 14,29,35,41,48，状态为源码确认。

Set 的成本只计 value 长度，空值条目仍进入 sync.Map 并保留键及元数据。现有 GoDoc 如实说明 payload 配额，调用方若将其当作堆内存上限会低估成本。 [store/cache/memory.go:233](../../../store/cache/memory.go#L233)

[复审报告](../../../docs/evidence/stable-quality-review-09a7f5d3.md)

## 验收与验证

- [x] 固定小 payload 限额比较多条空值、普通覆盖、Clear 后的成本和计数。
- [x] 记录验证命令、状态和剩余范围。若使用既有用例满足验收，记录对应证据。

```sh
go test -race -timeout 60s ./store/cache
go test ./store/cache -run '^$' -bench '^BenchmarkMemoryCacheEmptyEntries$' -benchmem -count=3
make website-sync
```

## 实施证据

[BenchmarkMemoryCacheEmptyEntries](../../../store/cache/memory_bench_test.go#L94)
