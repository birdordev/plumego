# Card 1769 — 锁定 Debug 已选错误状态的拒绝 Flush 行为

Milestone: 稳定模块质量复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: middleware
Owned Files: `middleware/debug/debug_errors_test.go`
Depends On: 无

Goal: 选定状态有一份原响应，未选仍允许后续结构化响应。
Scope: 构造不支持 Flush 的 writer，从未选和已选状态分别进入。
Non-goals: 维持稳定公开签名与标准库依赖，将验证限于本卡范围。
Files: `middleware/debug/debug_errors_test.go`。
Tests: 选定状态有一份原响应，未选仍允许后续结构化响应。
Docs Sync: 复核本卡涉及的已公开约定。
Done Definition: 完成下列验收，记录验证命令、状态及剩余限制。
Outcome: Flush 能力不支持时，已选状态按原纯文本路径提交，未选状态可继续进入 JSON 改写。

## 证据与影响

第 9 节检查号为 23,24,39。证据状态为补证。

无状态时拒绝 Flush 可回滚头；选定状态后会尝试提交旁路再 Flush。两类结果需各有验收。 [middleware/debug/debug_errors.go:226](../../../middleware/debug/debug_errors.go#L226)

[复审报告](../../../docs/evidence/stable-quality-review-d224acbc.md)

## 验收与验证

- [x] 选定状态有一份原响应，未选仍允许后续结构化响应。
- [x] 记录运行结果。若已有用例覆盖目标，记录用例及相关断言。

```sh
go test -race -timeout 60s ./middleware/debug
```

## 实施证据

- [TestUnsupportedDebugFlushPreservesSelectedStatusBoundary](../../../middleware/debug/debug_errors_test.go#L1197)

[批次验证与基准](../review-d224acbc/README.md)
