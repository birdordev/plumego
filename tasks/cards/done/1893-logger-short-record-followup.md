# Card 1893 — 核对底层短写后的诊断与后续完整记录

Milestone: 稳定模块质量复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: log
Owned Files: `log/safety_regression_test.go`
Depends On: 无

Goal: 可观测写入完整记录的次数、固定诊断和下一成功日志，无重复拼接。
Scope: 为文本与 JSON 共用可计数的合成 writer，在首次短写后恢复，捕获独立 stderr。
Non-goals: 维持稳定公开签名与标准库依赖，将验证限于本卡范围。
Files: `log/safety_regression_test.go`。
Tests: 可观测写入完整记录的次数、固定诊断和下一成功日志，无重复拼接。
Docs Sync: 记录核验结论；只在现有契约文字需要澄清时另提文档变更。
Done Definition: 完成下列验收，记录验证命令、状态及剩余限制。
Outcome: 文本和 JSON 底层短写产生固定诊断，后续写入只形成一条完整记录。

## 证据与影响

第 9 节检查号为 22,25,29,43。证据状态为补证。

写入诊断已有固定消息和频次验证。io.Writer 返回少量字节但无错误时，需要确认不会拼接重试或丢失下一记录。 [log/glog_writer.go:260](../../../log/glog_writer.go#L260)

[复审报告](../../../docs/evidence/stable-quality-review-a8c2f28a.md)

## 验收与验证

- [x] 可观测写入完整记录的次数、固定诊断和下一成功日志，无重复拼接。
- [x] 记录挂载入口、具体断言和结果。已有测试能覆盖时复用并注明。

```sh
go test -race -timeout 60s ./log
```

## 实施证据

- [TestShortRecordReportsOnceAndLeavesSecondRecordWhole](../../../log/safety_regression_test.go#L342)

[批次验证与基准](../review-a8c2f28a/README.md)
