# Card 2143 — 核对嵌套扇出中的标签隔离和独立重置

Milestone: 稳定模块第 9 节复审
Recipe: specs/change-recipes/analysis-only.yaml
Context Package: review
Priority: P3
State: done
Primary Module: metrics
Owned Files: `metrics/multi_test.go`
Depends On: 无

Goal: 核对嵌套扇出中的标签隔离和独立重置。
Scope: 添加能修改标签的合成叶子，复用稳定入口验证外层复制和子树边界。
Non-goals: 本卡提交取证结果。后续行为变更由独立修复卡执行。
Files: `metrics/multi_test.go`
Tests: 核对原标签、兄弟叶和后续记录互不污染，并按既有约定读取相互独立的子统计。
Docs Sync: 核对既有文档与验收结果一致，记录需要后续同步的位置。
Done Definition: 记录命令、结果、证据限制和是否需要独立修复卡。
Outcome: 嵌套 MultiCollector 含单个收集器直通，第一条记录计入三个叶子。重置嵌套子树后，外层独立叶仍保持原记录。嵌套叶修改标签的探针确认调用方和兄弟叶保留原值，既有标签隔离回归继续通过。

## 证据与影响

证据状态为待补证。第 9 节检查号为 31,32,34,44,45,47。已有平面扇出标签隔离及逐收集器 Clear 测试。嵌套多收集器含单目标直通时，叶结点所有权和独立统计待组合验证。 [metrics/multi.go:20](../../../metrics/multi.go#L20) [metrics/multi.go:47](../../../metrics/multi.go#L47) [metrics/multi.go:75](../../../metrics/multi.go#L75) [metrics/concurrency_regression_test.go:97](../../../metrics/concurrency_regression_test.go#L97)

[复审报告](../../../docs/evidence/stable-quality-review-6e8d53f9.md)

## 验收与验证

- [x] 核对原标签、兄弟叶和后续记录互不污染，并按既有约定读取相互独立的子统计。
- [x] 记录命令、结果、证据限制和是否需要独立修复卡。

```sh
go test -race -timeout 60s ./metrics
```
