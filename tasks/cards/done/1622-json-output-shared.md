# Card 1622 — 补证 JSON 衍生日志的共用输出串行化

Milestone: 本轮质量审查
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: log
Owned Files: `log/json_test.go`
Depends On: 无

Goal: 补证 JSON 衍生日志的共用输出串行化。
Scope: 复用短写夹具并以通道同时启动根 logger 和衍生 logger。
Non-goals: 保留现有公开签名、依赖与模块边界。测试若证实新的缺陷，先记录证据再单独安排修复。
Files: `log/json_test.go`。
Tests: 每行可独立解析，字段和级别对应，失败诊断至多一次。
Docs Sync: 本卡按现有契约验证。若取证表明契约需调整，先更新后续卡范围。
Done Definition: 完成下列验收和验证，记录证据与剩余风险。
Outcome: 根 logger 和衍生 logger 同步启动，逐行核对级别、消息和字段；既有回归验证共享写错误抑制。

## 证据与影响

- 检查号：32,33,43,47。证据状态：补证。
- 衍生 logger 共享输出锁，写入器可能分多次消费一行。 [log/json.go:165](../../../log/json.go#L165)
- [审查报告](../../../docs/evidence/stable-quality-review-dee7aa0f.md)

## 验收与验证

- [x] 每行可独立解析，字段和级别对应，失败诊断至多一次。
- [x] 运行以下命令并记录结果、失败路径和未验证范围。修改 Go 文件前完成仓库预检，验证前先执行仓库格式化。

```sh
go test -race -timeout 60s ./log
```

## 实施证据

[TestJSONFormatLoggerWithFieldsSharesWriterLock](../../../log/json_test.go#L714) · [TestJSONFormatLoggerWithFieldsSharesWriteErrorState](../../../log/json_test.go#L758) · [TestJSONLoggerReportWriteErrorOnce](../../../log/coverage_test.go#L272)
