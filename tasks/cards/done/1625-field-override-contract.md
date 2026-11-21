# Card 1625 — 锁定日志预置字段与临时字段优先级

Milestone: 本轮质量审查
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P3
State: done
Primary Module: log
Owned Files: `log/json_test.go`, `log/glog_test.go`
Depends On: 无

Goal: 锁定日志预置字段与临时字段优先级。
Scope: 核对现有断言，使用相同字段输入对照文本与 JSON。
Non-goals: 保留现有公开签名、依赖与模块边界。测试若证实新的缺陷，先记录证据再单独安排修复。
Files: `log/json_test.go`, `log/glog_test.go`。
Tests: 衍生字段和本次字段冲突顺序明确，JSON 的保留键始终有效。
Docs Sync: 本卡按现有契约验证。若取证表明契约需调整，先更新后续卡范围。
Done Definition: 完成下列验收和验证，记录证据与剩余风险。
Outcome: 复用文本、JSON 和共享字段覆盖测试，观察本次字段覆盖预置字段及 JSON 保留键不变量。

## 证据与影响

- 检查号：6,16,34,43。证据状态：补证。
- 两种格式共用字段合并，JSON 保留名另由内部覆盖。 [log/fields.go:70](../../../log/fields.go#L70)
- [审查报告](../../../docs/evidence/stable-quality-review-dee7aa0f.md)

## 验收与验证

- [x] 衍生字段和本次字段冲突顺序明确，JSON 的保留键始终有效。
- [x] 运行以下命令并记录结果、失败路径和未验证范围。修改 Go 文件前完成仓库预检，验证前先执行仓库格式化。

```sh
go test -race -timeout 60s ./log
```

## 实施证据

[TestTextLoggerMergesVariadicFieldsInOrder](../../../log/logger_semantics_test.go#L131) · [TestJSONFormatLoggerFieldOverride](../../../log/json_test.go#L489) · [TestJSONFormatLoggerReservedFieldsCannotOverrideCoreKeys](../../../log/json_test.go#L554)
