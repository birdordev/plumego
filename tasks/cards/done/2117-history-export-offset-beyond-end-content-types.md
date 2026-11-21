# Card 2117 — 历史导出过末页的 JSON 与 CSV 形状

Milestone: 稳定模块第 9 节复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: x/observability/ops
Owned Files: `x/observability/ops/healthhttp/history_test.go`
Depends On: 无

Goal: 历史导出过末页的 JSON 与 CSV 形状
Scope: 用同一简单历史和超末尾 offset 比较两种导出。
Non-goals: 范围限于本卡涉及的契约，保持公开签名和依赖。
Files: `x/observability/ops/healthhttp/history_test.go`。
Tests: 保持总数、空 JSON 条目、CSV 表头及正确 Content-Type。
Docs Sync: 核对新增验收与已有模块说明一致，记录证据和限制。
Done Definition: 完成以下验收，记录命令、结果与剩余限制。
Outcome: 末页之外的导出保留 JSON 总数及显式空数组，CSV 仅一行表头，两种 Content-Type 正确。

## 证据与影响

证据状态为补证。第 9 节检查号为 6,21,24,38,42。 [质量规则第 9 节](../../../docs/operations/agent-code-quality-rules.md#L267)

过滤分页对空结果仍返回合法结构，CSV 有表头。 [x/observability/ops/healthhttp/history.go:27](../../../x/observability/ops/healthhttp/history.go#L27)

[复审报告](../../../docs/evidence/stable-quality-review-2f962d21.md)

## 验收与验证

- [x] 保持总数、空 JSON 条目、CSV 表头及正确 Content-Type。
- [x] 已有测试覆盖相同触发和断言时可直接复用，记录具体用例。

```sh
go test -race -timeout 60s ./x/observability/ops/healthhttp
```

## 实施证据

- [TestHistoryExportPastEndKeepsTotalAndCSVHeader](../../../x/observability/ops/healthhttp/history_test.go#L552)
