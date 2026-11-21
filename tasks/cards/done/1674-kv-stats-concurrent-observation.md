# Card 1674 — 核对 GetStats 同次读到的命中与未命中总计

Milestone: 稳定模块质量复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: store
Owned Files: `store/kv/kv_test.go`
Depends On: 无

Goal: 核对 GetStats 同次读到的命中与未命中总计。
Scope: 通过通道并行运行读取与统计，定义哪些字段需要同刻可见。
Non-goals: 维持公开签名和依赖；涉及现有兼容契约的调整须先核对调用边界及迁移影响。
Files: `store/kv/kv_test.go`。
Tests: 操作静止后计数准确，中途统计的允许区间可解释。
Docs Sync: 对照原契约核验，若证据要求修订公开行为，先更新实施范围。
Done Definition: 完成测试与验收记录，并标明剩余取证范围。
Outcome: 并行读取期间统计有界，读取全部完成后命中与未命中总计精确。

## 证据

检查号为 31,32,34,41，状态为补证。

命中和未命中原子独立读写，条目和容量随锁快照。 [store/kv/kv.go:356](../../../store/kv/kv.go#L356)

[复审报告](../../../docs/evidence/stable-quality-review-09a7f5d3.md)

## 验收与验证

- [x] 操作静止后计数准确，中途统计的允许区间可解释。
- [x] 记录验证命令、状态和剩余范围。若使用既有用例满足验收，记录对应证据。

```sh
go test -race -timeout 60s ./store/kv
```

## 实施证据

[TestStatsDuringConcurrentReadsAndAfterCompletion](../../../store/kv/kv_test.go#L1862)
