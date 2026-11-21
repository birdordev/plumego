# Card 1577 — 默认指标收集器没有名称基数上限

Milestone: 本轮质量审查
Recipe: specs/change-recipes/fix-bug.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: metrics
Owned Files: `metrics/collector.go`, `metrics/collector_test.go`, `docs/modules/metrics/README.md`, `website/src/generated/modules.ts`
Depends On: 无

Goal: 默认指标收集器没有名称基数上限。
Scope: 先明确固定名称的调用契约并盘点代表调用点；经兼容决策后再选择是否提供受限收集方式。
Non-goals: 保留现有公开签名和依赖。涉及未授权的行为或兼容契约调整时，先完成调用方核对与兼容决策。
Files: `metrics/collector.go`, `metrics/collector_test.go`, `docs/modules/metrics/README.md`, `website/src/generated/modules.ts`。
Tests: 比较固定名称和逐条变化名称的 NameBreakdown、分配和 Clear 后状态，记录经批准的容量策略。
Docs Sync: 核对 `docs/modules/metrics/README.md`。 修改模块说明后检查网站生成差异，并在同一交付中保留相应生成文件。
Done Definition: 完成下列验收和验证，记录证据与剩余风险。
Outcome: 文档明确每个非空名称保留到 Clear，推荐固定名称集合；保持已有收集器接口和统计结果。基准同时验证基数与 Clear。

## 证据与影响

- 检查号：14,35,44,48。证据状态：源码确认。
- Record 接受任意非空名称，每个新名称进入 NameBreakdown 并保留到 Clear。调用者把变化中的值作为名称时，内存与快照分配持续增长。 [metrics/collector.go:148](../../../metrics/collector.go#L148)
- [审查报告](../../../docs/evidence/stable-quality-review-dee7aa0f.md)

## 验收与验证

- [x] 比较固定名称和逐条变化名称的 NameBreakdown、分配和 Clear 后状态，记录经批准的容量策略。
- [x] 运行以下命令并记录结果、失败路径和未验证范围。修改 Go 文件前完成仓库预检，验证前先执行仓库格式化。

```sh
go test -race -timeout 60s ./metrics
make website-sync
make website-check-generated
```

## 实施证据

[BenchmarkBaseCollectorNameCardinality](../../../metrics/bench_test.go#L75)
