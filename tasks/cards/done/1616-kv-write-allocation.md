# Card 1616 — 测量 KV 写入规模与峰值

Milestone: 本轮质量审查
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: store
Owned Files: `store/kv/kv_bench_test.go`
Depends On: 无

Goal: 测量 KV 写入规模与峰值。
Scope: 扩展基准大小阶梯，固定 payload 并记录 go env 与文件系统。
Non-goals: 保留现有公开签名、依赖与模块边界。测试若证实新的缺陷，先记录证据再单独安排修复。
Files: `store/kv/kv_bench_test.go`。
Tests: 保存可复跑的 ns/op、B/op、allocs/op 与快照大小，禁止改变格式以换取基准结果。
Docs Sync: 本卡按现有契约验证。若取证表明契约需调整，先更新后续卡范围。
Done Definition: 完成下列验收和验证，记录证据与剩余风险。
Outcome: 按 10、100、1000 条历史数据测量完整写入的耗时、分配与快照大小。

## 证据与影响

- 检查号：29,35,41,48。证据状态：补证。
- 每次写完整编码、同步与替换，当前审查未量化规模增加的成本。 [store/kv/persistence.go:166](../../../store/kv/persistence.go#L166)
- [审查报告](../../../docs/evidence/stable-quality-review-dee7aa0f.md)

## 验收与验证

- [x] 保存可复跑的 ns/op、B/op、allocs/op 与快照大小，禁止改变格式以换取基准结果。
- [x] 运行以下命令并记录结果、失败路径和未验证范围。修改 Go 文件前完成仓库预检，验证前先执行仓库格式化。

```sh
go test -race -timeout 60s ./store/kv
go test ./store/kv -run '^$' -bench 'BenchmarkKVWriteSnapshotScale' -benchmem -count=5
```

## 实施证据

[BenchmarkKVWriteSnapshotScale](../../../store/kv/kv_bench_test.go#L70)
