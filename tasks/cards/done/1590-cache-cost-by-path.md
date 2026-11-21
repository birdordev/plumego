# Card 1590 — 测量缓存路径长度和 FIFO 驱逐成本

Milestone: 本轮质量审查
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: router
Owned Files: `router/router_bench_test.go`
Depends On: 无

Goal: 测量缓存路径长度和 FIFO 驱逐成本。
Scope: 扩展已有冷热 benchmark，使用固定种子和路径集合。
Non-goals: 保留现有公开签名、依赖与模块边界。测试若证实新的缺陷，先记录证据再单独安排修复。
Files: `router/router_bench_test.go`。
Tests: 报告不同路径长度下的 ns/op、B/op、allocs/op 和命中差异，同时检查路由一致。
Docs Sync: 本卡按现有契约验证。若取证表明契约需调整，先更新后续卡范围。
Done Definition: 完成下列验收和验证，记录证据与剩余风险。
Outcome: 新增路由缓存长度和 FIFO 工作集基准，验证分派一致并记录时间与分配。

## 证据与影响

- 检查号：35,37,48。证据状态：补证。
- 固定容量按条目数计算，路径长短和热点分布影响保留内存和命中速度。 [router/cache.go:11](../../../router/cache.go#L11)
- [审查报告](../../../docs/evidence/stable-quality-review-dee7aa0f.md)

## 验收与验证

- [x] 报告不同路径长度下的 ns/op、B/op、allocs/op 和命中差异，同时检查路由一致。
- [x] 运行以下命令并记录结果、失败路径和未验证范围。修改 Go 文件前完成仓库预检，验证前先执行仓库格式化。

```sh
go test -race -timeout 60s ./router
go test ./router -run '^$' -bench 'BenchmarkOptFrozenVsUnfrozen|BenchmarkRouteCachePathLength' -benchmem -count=5
```

## 实施证据

[BenchmarkRouteCachePathLength](../../../router/router_bench_test.go#L358)
