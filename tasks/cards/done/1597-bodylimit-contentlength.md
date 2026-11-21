# Card 1597 — 补证请求体上限的已声明长度与惰性读取

Milestone: 本轮质量审查
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: middleware
Owned Files: `middleware/bodylimit/body_limit_test.go`
Depends On: 无

Goal: 补证请求体上限的已声明长度与惰性读取。
Scope: 补读取者、忽略正文者和短读者三种公开链测试，确认是否需文档说明惰性限制。
Non-goals: 保留现有公开签名、依赖与模块边界。测试若证实新的缺陷，先记录证据再单独安排修复。
Files: `middleware/bodylimit/body_limit_test.go`。
Tests: 读取不超限时正常通过，超限被发现时只产生一次 413，未读正文依既有契约交给 handler。
Docs Sync: 本卡按现有契约验证。若取证表明契约需调整，先更新后续卡范围。
Done Definition: 完成下列验收和验证，记录证据与剩余风险。
Outcome: 对照已声明长度、未读、部分读和过量实际读，保持惰性上限契约。

## 证据与影响

- 检查号：18,20,35,39。证据状态：补证。
- 实现按实际读取消耗限额，Content-Length 只是请求元数据。 [middleware/bodylimit/body_limit.go:49](../../../middleware/bodylimit/body_limit.go#L49)
- [审查报告](../../../docs/evidence/stable-quality-review-dee7aa0f.md)

## 验收与验证

- [x] 读取不超限时正常通过，超限被发现时只产生一次 413，未读正文依既有契约交给 handler。
- [x] 运行以下命令并记录结果、失败路径和未验证范围。修改 Go 文件前完成仓库预检，验证前先执行仓库格式化。

```sh
go test -race -timeout 60s ./middleware/bodylimit
```

## 实施证据

[TestBodyLimitEnforcesBytesReadIndependentlyOfDeclaredLength](../../../middleware/bodylimit/body_limit_test.go#L657)
