# Card 1620 — 复核就绪状态在宿主中的终止边界

Milestone: 本轮质量审查
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: x/observability
Owned Files: `x/observability/ops/healthhttp/manual_generation_test.go`, `x/observability/ops/healthhttp/aggregate_regression_test.go`
Depends On: 无

Goal: 复核就绪状态在宿主中的终止边界。
Scope: 复用消费方回归，验证同一实例先有探测在途、再 Close、再手工更新、最后释放探测的交错。
Non-goals: 保留现有公开签名、依赖与模块边界。测试若证实新的缺陷，先记录证据再单独安排修复。
Files: `x/observability/ops/healthhttp/manual_generation_test.go`, `x/observability/ops/healthhttp/aggregate_regression_test.go`。
Tests: Close 返回后手工与检查结果均不能使 readiness 恢复；稳定 health 模型无需改动。
Docs Sync: 本卡按现有契约验证。若取证表明契约需调整，先更新后续卡范围。
Done Definition: 完成下列验收和验证，记录证据与剩余风险。
Outcome: 编排方用受控探测验证关闭后手工更新和在途结果都无法恢复就绪。

## 证据与影响

- 检查号：28,32,42,47。证据状态：补证。
- 稳定模型不编排检查，现有消费方分别覆盖关闭后的手工更新和在途探测。组合交错仍需证据。 [x/observability/ops/healthhttp/manager.go:443](../../../x/observability/ops/healthhttp/manager.go#L443)
- [审查报告](../../../docs/evidence/stable-quality-review-dee7aa0f.md)

## 验收与验证

- [x] Close 返回后手工与检查结果均不能使 readiness 恢复；稳定 health 模型无需改动。
- [x] 运行以下命令并记录结果、失败路径和未验证范围。修改 Go 文件前完成仓库预检，验证前先执行仓库格式化。

```sh
go test -race -timeout 60s ./x/observability/ops/healthhttp
```

## 实施证据

[TestCloseRejectsManualUpdatesWithProbeInFlight](../../../x/observability/ops/healthhttp/manual_generation_test.go#L75)
