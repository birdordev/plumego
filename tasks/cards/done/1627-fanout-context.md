# Card 1627 — 补证 fan-out 传播调用上下文身份

Milestone: 本轮质量审查
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: metrics
Owned Files: `metrics/concurrency_regression_test.go`
Depends On: 无

Goal: 补证 fan-out 传播调用上下文身份。
Scope: 复用并发回归的记录器，断言上下文和标签两个维度。
Non-goals: 保留现有公开签名、依赖与模块边界。测试若证实新的缺陷，先记录证据再单独安排修复。
Files: `metrics/concurrency_regression_test.go`。
Tests: 子收集器见到原上下文取消，修改标签不影响任何并行消费者。
Docs Sync: 本卡按现有契约验证。若取证表明契约需调整，先更新后续卡范围。
Done Definition: 完成下列验收和验证，记录证据与剩余风险。
Outcome: 验证 fan-out 子收集器见到原上下文取消，标签不受同级修改影响。

## 证据与影响

- 检查号：26,27,34,44。证据状态：补证。
- 标签克隆已有测试，子收集器收到调用上下文与其取消状态需一起验证。 [metrics/multi.go:58](../../../metrics/multi.go#L58)
- [审查报告](../../../docs/evidence/stable-quality-review-dee7aa0f.md)

## 验收与验证

- [x] 子收集器见到原上下文取消，修改标签不影响任何并行消费者。
- [x] 运行以下命令并记录结果、失败路径和未验证范围。修改 Go 文件前完成仓库预检，验证前先执行仓库格式化。

```sh
go test -race -timeout 60s ./metrics
```

## 实施证据

[TestFanoutPreservesCallerContextAndIsolatesLabels](../../../metrics/concurrency_regression_test.go#L132)
