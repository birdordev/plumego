# Card 1624 — 补证文本日志多目的地短写

Milestone: 本轮质量审查
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: log
Owned Files: `log/glog_test.go`
Depends On: 无

Goal: 补证文本日志多目的地短写。
Scope: 用两个仅在内存运行的目的地记录写入和失败。
Non-goals: 保留现有公开签名、依赖与模块边界。测试若证实新的缺陷，先记录证据再单独安排修复。
Files: `log/glog_test.go`。
Tests: 单个目的地失败时诊断稳定且原始错误内容不进入诊断，另一目的地按契约输出。
Docs Sync: 本卡按现有契约验证。若取证表明契约需调整，先更新后续卡范围。
Done Definition: 完成下列验收和验证，记录证据与剩余风险。
Outcome: 复用多目的地短写和诊断限次测试，文本日志在文件失败处停止后续输出，错误诊断保持固定文本。

## 证据与影响

- 检查号：22,30,43,46。证据状态：补证。
- 文本写入可能应用多个目的地，主目的地出现短写时需明确另一目的地结果。 [log/glog_writer.go:255](../../../log/glog_writer.go#L255)
- [审查报告](../../../docs/evidence/stable-quality-review-dee7aa0f.md)

## 验收与验证

- [x] 单个目的地失败时诊断稳定且原始错误内容不进入诊断，另一目的地按契约输出。
- [x] 运行以下命令并记录结果、失败路径和未验证范围。修改 Go 文件前完成仓库预检，验证前先执行仓库格式化。

```sh
go test -race -timeout 60s ./log
```

## 实施证据

[TestLogDestinationsCountShortFileWrite](../../../log/glog_test.go#L1099) · [TestGLoggerReportWriteErrorOnlyOnce](../../../log/coverage_test.go#L408)
