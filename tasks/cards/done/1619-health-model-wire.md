# Card 1619 — 补证未知状态模型的序列化与判断

Milestone: 本轮质量审查
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: health
Owned Files: `health/json_contract_test.go`
Depends On: 无

Goal: 补证未知状态模型的序列化与判断。
Scope: 将模型 JSON 测试与 IsReady/IsKnown 的断言组合。
Non-goals: 保留现有公开签名、依赖与模块边界。测试若证实新的缺陷，先记录证据再单独安排修复。
Files: `health/json_contract_test.go`。
Tests: 零值、未知值和三种已知值的序列化及判定符合模型契约。
Docs Sync: 本卡按现有契约验证。若取证表明契约需调整，先更新后续卡范围。
Done Definition: 完成下列验收和验证，记录证据与剩余风险。
Outcome: 同一表用例对照未知、零值和稳定状态的 JSON、IsKnown 与 IsReady。

## 证据与影响

- 检查号：6,16,42,45。证据状态：补证。
- 未知状态不就绪，模型作为字符串仍可传输扩展状态。 [health/core.go:68](../../../health/core.go#L68)
- [审查报告](../../../docs/evidence/stable-quality-review-dee7aa0f.md)

## 验收与验证

- [x] 零值、未知值和三种已知值的序列化及判定符合模型契约。
- [x] 运行以下命令并记录结果、失败路径和未验证范围。修改 Go 文件前完成仓库预检，验证前先执行仓库格式化。

```sh
go test -race -timeout 60s ./health
```

## 实施证据

[TestHealthStateWireAndReadinessAgree](../../../health/json_contract_test.go#L168)
