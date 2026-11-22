# Card 2132 — 核对无正文 JSON 响应在 103 后的最终提交

Milestone: 稳定模块第 9 节复审
Recipe: specs/change-recipes/analysis-only.yaml
Context Package: review
Priority: P2
State: done
Primary Module: contract
Owned Files: `contract/response_test.go`
Depends On: 无

Goal: 核对无正文 JSON 响应在 103 后的最终提交。
Scope: 经记录 1xx 和最终提交的 writer 用同一表覆盖 204、205 与普通 2xx。
Non-goals: 本卡提交取证结果。后续行为变更由独立修复卡执行。
Files: `contract/response_test.go`
Tests: 断言最终状态一次、204/205 跳过编码、204 无长度、205 长度零，并保留无关字段。
Docs Sync: 核对既有文档与验收结果一致，记录需要后续同步的位置。
Done Definition: 记录命令、结果、证据限制和是否需要独立修复卡。
Outcome: 记录器探针确认 103 后 204/205 各仅提交一次最终状态，跳过不可编码的值，清理 Transfer-Encoding，分别省略长度或报告 0，保留无关头字段。

## 证据与影响

证据状态为待补证。第 9 节检查号为 17,21,23,24,38,45。204/205 有独立响应回归。先发送 103，再调用 WriteResponse 的组合中，临时头、最终 framing 和用户编码器调用次数待核对。 [contract/response.go:22](../../../contract/response.go#L22) [contract/response_test.go:53](../../../contract/response_test.go#L53) [contract/response_test.go:711](../../../contract/response_test.go#L711)

[复审报告](../../../docs/evidence/stable-quality-review-6e8d53f9.md)

## 验收与验证

- [x] 断言最终状态一次、204/205 跳过编码、204 无长度、205 长度零，并保留无关字段。
- [x] 记录命令、结果、证据限制和是否需要独立修复卡。

```sh
go test -race -timeout 60s ./contract
```
