# Card 1842 — 核对文本后端错误 Stringer 与控制字符的边界

Milestone: 稳定模块质量复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: log
Owned Files: `log/safety_regression_test.go`
Depends On: 无

Goal: 一条完整日志、无原始控制字符、整型不降精度。
Scope: 使用人工 Stringer 与无害控制符标本，仅检查序列化。
Non-goals: 维持稳定公开签名与标准库依赖，将验证限于本卡范围。
Files: `log/safety_regression_test.go`。
Tests: 一条完整日志、无原始控制字符、整型不降精度。
Docs Sync: 复核本卡涉及的已公开约定。
Done Definition: 完成下列验收，记录验证命令、状态及剩余限制。
Outcome: 文本错误字段转义控制符及无效字节，同行保留大整数精度。

## 证据与影响

第 9 节检查号为 25,33,43,46。证据状态为补证。

文本日志支持多级字段值，已有循环和控制符单测。组合普通错误、无效字符串和整数保真需要单独验证。 [log/glog_writer.go:268](../../../log/glog_writer.go#L268)

[复审报告](../../../docs/evidence/stable-quality-review-a074eadb.md)

## 验收与验证

- [x] 一条完整日志、无原始控制字符、整型不降精度。
- [x] 记录运行结果。若已有用例覆盖目标，记录用例及相关断言。

```sh
go test -race -timeout 60s ./log
```

## 实施证据

- [TestTextErrorFieldEscapesControlsAndKeepsSiblingInteger](../../../log/safety_regression_test.go#L317)

[批次验证与基准](../review-a074eadb/README.md)
