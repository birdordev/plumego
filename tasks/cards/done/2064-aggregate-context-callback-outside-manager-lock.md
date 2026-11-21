# Card 2064 — 批次发布在管理器锁内调用上下文方法

Milestone: 稳定模块第 9 节复审
Recipe: specs/change-recipes/fix-bug.yaml
Context Package: implementation
Priority: P1
State: done
Primary Module: x/observability/ops
Owned Files: `x/observability/ops/healthhttp/manager.go`, `x/observability/ops/healthhttp/aggregate_regression_test.go`
Depends On: 无

Goal: 批次发布在管理器锁内调用上下文方法
Scope: 在锁外读取上下文信号和错误。发布持锁时只查看已取得的信号、版本和关闭状态。若取消刚到而错误类型尚未读取，释放锁采样错误，再重新校验发布资格。
Non-goals: 范围限于本卡涉及的契约，保持公开签名和依赖。
Files: `x/observability/ops/healthhttp/manager.go`, `x/observability/ops/healthhttp/aggregate_regression_test.go`。
Tests: 受控上下文在 Err 中读取配置后，空批次可正常结束。用屏障分别核验取消类型、较新批次、手动就绪和关闭仍拥有各自的发布优先级。
Docs Sync: 修复时核对模块说明。若现有文档与新行为不一致，记录对应同步项。
Done Definition: 完成以下验收，记录命令、结果与剩余限制。
Outcome: 完成。批次在锁外读取上下文信号和错误，取消晚到时在重新锁定后复查发布资格。公开重入探针已通过。

## 证据与影响

证据状态为已复现。第 9 节检查号为 26,32,33,42,45,47。 [质量规则第 9 节](../../../docs/operations/agent-code-quality-rules.md#L267)

空组件批次在最终发布段持有写锁调用传入的 ctx.Err。同包探针观察到读锁不可用，走公开接口的隔离进程中，Err 在读取 GetConfig 时互等至 2 秒测试超时。标准库上下文路径未观察到该阻塞。 [x/observability/ops/healthhttp/manager.go:378](../../../x/observability/ops/healthhttp/manager.go#L378)

[复审报告](../../../docs/evidence/stable-quality-review-2f962d21.md)

## 验收与验证

- [x] 受控上下文在 Err 中读取配置后，空批次可正常结束。用屏障分别核验取消类型、较新批次、手动就绪和关闭仍拥有各自的发布优先级。
- [x] 已有测试覆盖相同触发和断言时可直接复用，记录具体用例。

```sh
go test -race -timeout 60s ./x/observability/ops/healthhttp
```

## 实施证据

- [TestBatchPublicationContextCanReadManagerConfig](../../../x/observability/ops/healthhttp/aggregate_regression_test.go#L354)
- [TestBatchPublicationRevalidatesAfterRefreshingCancellation](../../../x/observability/ops/healthhttp/aggregate_regression_test.go#L378)

同一回归在 2f962d21 的原实现上失败，修复后通过。
