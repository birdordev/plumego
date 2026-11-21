# Card 1821 — 核对读取超限与已提交成功头后的 Flush

Milestone: 稳定模块质量复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: middleware
Owned Files: `middleware/bodylimit/body_limit_test.go`
Depends On: 无

Goal: 成功头与前缀保留，后续 Flush 返回可分类错误。
Scope: 显式选择状态、写普通前缀、读到超限、尝试 Flush。
Non-goals: 维持稳定公开签名与标准库依赖，将验证限于本卡范围。
Files: `middleware/bodylimit/body_limit_test.go`。
Tests: 成功头与前缀保留，后续 Flush 返回可分类错误。
Docs Sync: 复核本卡涉及的已公开约定。
Done Definition: 完成下列验收，记录验证命令、状态及剩余限制。
Outcome: 超限后保留已提交的成功头和正文前缀，Flush 返回超限错误且不调用底层刷新。

## 证据与影响

第 9 节检查号为 20,23,24,39。证据状态为补证。

现有卡处理被阻断的 Write。部分响应已经发送时，Flush 与底层计数仍需组合作证。 [middleware/bodylimit/body_limit.go:118](../../../middleware/bodylimit/body_limit.go#L118)

[复审报告](../../../docs/evidence/stable-quality-review-a074eadb.md)

## 验收与验证

- [x] 成功头与前缀保留，后续 Flush 返回可分类错误。
- [x] 记录运行结果。若已有用例覆盖目标，记录用例及相关断言。

```sh
go test -race -timeout 60s ./middleware/bodylimit
```

## 实施证据

- [TestOverrunAfterSuccessPreservesPrefixAndBlocksFlush](../../../middleware/bodylimit/body_limit_test.go#L832)

[批次验证与基准](../review-a074eadb/README.md)
