# Card 1631 — 指标模块说明误称默认收集器保留耗时

Milestone: 本轮质量审查
Recipe: specs/change-recipes/fix-bug.yaml
Context Package: implementation
Priority: P3
State: done
Primary Module: metrics
Owned Files: `docs/modules/metrics/README.md`, `website/src/generated/modules.ts`
Depends On: 无

Goal: 指标模块说明误称默认收集器保留耗时。
Scope: 改写模块说明，分别描述基础计数聚合与标准 HTTP 记录。
Non-goals: 保留现有公开签名和依赖。涉及未授权的行为或兼容契约调整时，先完成调用方核对与兼容决策。
Files: `docs/modules/metrics/README.md`, `website/src/generated/modules.ts`。
Tests: 核对 metrics/collector.go 中两个入口及现有测试，生成网站模块索引并检查差异（若索引内容受影响）。
Docs Sync: 核对 `docs/modules/metrics/README.md`。 修改模块说明后检查网站生成差异，并在同一交付中保留相应生成文件。
Done Definition: 完成下列验收和验证，记录证据与剩余风险。
Outcome: 模块文档明确 ObserveHTTP 只计数，NewHTTPRecord 构造带耗时和时间戳的记录。

## 证据与影响

- 检查号：6,16,44,52。证据状态：源码确认。
- 模块说明把 ObserveHTTP 的采样形状和 BaseMetricsCollector 的聚合能力混写。默认收集器仅更新名称和错误计数，构建包含秒数的记录是 NewHTTPRecord 的职责。 [docs/modules/metrics/README.md:68](../../../docs/modules/metrics/README.md#L68)
- [审查报告](../../../docs/evidence/stable-quality-review-dee7aa0f.md)

## 验收与验证

- [x] 核对 metrics/collector.go 中两个入口及现有测试，生成网站模块索引并检查差异（若索引内容受影响）。
- [x] 运行以下命令并记录结果、失败路径和未验证范围。修改 Go 文件前完成仓库预检，验证前先执行仓库格式化。

```sh
make website-sync
make website-check-generated
git diff --check
```

## 实施证据

[TestBaseMetricsCollectorObserveHTTPMatchesRecord](../../../metrics/collector_test.go#L60) · [TestNewHTTPRecordUsesCanonicalShape](../../../metrics/collector_test.go#L77)
