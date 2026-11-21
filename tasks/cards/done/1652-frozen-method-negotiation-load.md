# Card 1652 — 度量冻结路由的 Allow 查询成本

Milestone: 稳定模块质量复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: router
Owned Files: `router/router_bench_test.go`
Depends On: 无

Goal: 度量冻结路由的 Allow 查询成本。
Scope: 基准分别布置若干方法和回溯分支，记录失败路径增长趋势。
Non-goals: 维持公开签名和依赖；涉及现有兼容契约的调整须先核对调用边界及迁移影响。
Files: `router/router_bench_test.go`。
Tests: 同批核对 Allow 去重顺序、未知路径 404 和 405 语义。
Docs Sync: 对照原契约核验，若证据要求修订公开行为，先更新实施范围。
Done Definition: 完成测试与验收记录，并标明剩余取证范围。
Outcome: 测量 2、16、64 个方法的冻结路由 Allow 查询，包含失败分支回溯及 404/405 对照。

## 证据

检查号为 24,35,37,48，状态为补证。

405 不复用 successful match cache，并需要遍历树和排序。 [router/dispatch.go:318](../../../router/dispatch.go#L318)

[复审报告](../../../docs/evidence/stable-quality-review-09a7f5d3.md)

## 验收与验证

- [x] 同批核对 Allow 去重顺序、未知路径 404 和 405 语义。
- [x] 记录验证命令、状态和剩余范围。若使用既有用例满足验收，记录对应证据。

```sh
go test -race -timeout 60s ./router
go test ./router -run '^$' -bench '^BenchmarkFrozenAllowLookup$' -benchmem -count=3
```

## 实施证据

[BenchmarkFrozenAllowLookup](../../../router/router_bench_test.go#L385)
