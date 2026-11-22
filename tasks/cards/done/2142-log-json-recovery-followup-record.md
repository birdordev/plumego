# Card 2142 — 核对 JSON 编码器异常后的后续同级记录

Milestone: 稳定模块第 9 节复审
Recipe: specs/change-recipes/analysis-only.yaml
Context Package: review
Priority: P3
State: done
Primary Module: log
Owned Files: `log/json_test.go`
Depends On: 无

Goal: 核对 JSON 编码器异常后的后续同级记录。
Scope: 同一父及派生 logger 连续写异常和正常字段，逐行解析，核对共享 writer 与序列编码边界。
Non-goals: 本卡提交取证结果。后续行为变更由独立修复卡执行。
Files: `log/json_test.go`
Tests: 检查两条独立 JSON 行、固定占位、保留整数和正常记录的上下文字段。
Docs Sync: 核对既有文档与验收结果一致，记录需要后续同步的位置。
Done Definition: 记录命令、结果、证据限制和是否需要独立修复卡。
Outcome: 同一 sink 先写自定义编码器 panic，再由派生 logger 写正常记录。逐行 JSON 解析保留异常占位、后续作用域与精确整数。

## 证据与影响

证据状态为待补证。第 9 节检查号为 25,29,32,43,45,46。已有单条自定义编码器异常与独立 writer 失败测试。异常字段所在记录与同一 sink 的下一条正常记录组合尚待核对。 [log/json.go:137](../../../log/json.go#L137) [log/json.go:282](../../../log/json.go#L282) [log/json_test.go:791](../../../log/json_test.go#L791) [log/json_test.go:923](../../../log/json_test.go#L923)

[复审报告](../../../docs/evidence/stable-quality-review-6e8d53f9.md)

## 验收与验证

- [x] 检查两条独立 JSON 行、固定占位、保留整数和正常记录的上下文字段。
- [x] 记录命令、结果、证据限制和是否需要独立修复卡。

```sh
go test -race -timeout 60s ./log
```
