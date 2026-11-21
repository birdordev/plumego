# Card 2061 — 并发注册与健康报告快照一致性

Milestone: 稳定模块第 9 节复审
Recipe: specs/change-recipes/analysis-only.yaml
Context Package: review
Priority: P2
State: done
Primary Module: x/observability/ops
Owned Files: `x/observability/ops/healthhttp/metrics_test.go`
Depends On: 无

Goal: 并发注册与健康报告快照一致性
Scope: 分析内置与注入 Manager；用可控注册变更的假实现记录报告字段来自各读取点。
Non-goals: 范围限于本卡涉及的契约，保持公开签名和依赖。
Files: `x/observability/ops/healthhttp/metrics_test.go`。
Tests: 说明一致性边界及实际调用方需求。需要改变契约时另建明确迁移卡。
Docs Sync: 核对新增验收与已有模块说明一致，记录证据和限制。
Done Definition: 完成以下验收，记录命令、结果与剩余限制。
Outcome: 仅分析。注入 Manager 在读取列表后注销组件，报告仍包含旧名称，之后读取的管理器已为空。

## 证据与影响

证据状态为补证。第 9 节检查号为 28,32,34,42,47。 [质量规则第 9 节](../../../docs/operations/agent-code-quality-rules.md#L267)

报告分别获取组件名、整体健康和就绪，静态上可能跨版本。公开接口未承诺整体原子性。 [x/observability/ops/healthhttp/tracker.go:543](../../../x/observability/ops/healthhttp/tracker.go#L543)

[复审报告](../../../docs/evidence/stable-quality-review-c3a77410.md)

## 验收与验证

- [x] 说明一致性边界及实际调用方需求。需要改变契约时另建明确迁移卡。
- [x] 验收以记录观察结果和兼容性判断为准。

## 实施证据

用注入 Manager 在返回组件列表前注销唯一组件。报告仍列出该组件，而随后请求聚合与就绪时管理器已无此组件。分次读取的边界同样存在于内置调用路径，原子性没有公开约定。 临时探针通过，工作区内未新增对应实现。

```sh
go test -race -count=1 -v -overlay=/tmp/c3a-impl-analysis-overlay.json ./x/observability/ops/healthhttp -run '^TestReviewReportMayMixManagerSnapshots$'
```
