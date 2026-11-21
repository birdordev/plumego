# Card 1799 — 健康重试在等待期间丢失取消原因

Milestone: 稳定模块质量复审
Recipe: specs/change-recipes/fix-bug.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: x/observability
Owned Files: `x/observability/ops/healthhttp/manager.go`, `x/observability/ops/healthhttp/component_regression_test.go`, `docs/modules/x/observability/ops/README.md`
Depends On: 无

Goal: 同一个合成依赖对照检查中取消、退避中取消和普通重试，验证错误链与记录次数。
Scope: 取消中断重试时返回 ctx.Err，尝试次数保持已执行次数。
Non-goals: 维持稳定公开签名与标准库依赖，将验证限于本卡范围。
Files: `x/observability/ops/healthhttp/manager.go`, `x/observability/ops/healthhttp/component_regression_test.go`, `docs/modules/x/observability/ops/README.md`。
Tests: 同一个合成依赖对照检查中取消、退避中取消和普通重试，验证错误链与记录次数。
Docs Sync: 复核模块说明并验证生成数据。
Done Definition: 完成下列验收，记录验证命令、状态及剩余限制。
Outcome: 退避中的取消返回 Context 原因，尝试数保持已执行次数。

## 证据与影响

第 9 节检查号为 22,26,42,46。证据状态为已复现。

合成检查失败后进入重试等待，取消 Context 时返回上一次 checker 错误。若取消在检查返回时已可见，则同一函数返回 Context 错误。 [x/observability/ops/healthhttp/manager.go:484](../../../x/observability/ops/healthhttp/manager.go#L484)

[复审报告](../../../docs/evidence/stable-quality-review-a074eadb.md)

## 验收与验证

- [x] 同一个合成依赖对照检查中取消、退避中取消和普通重试，验证错误链与记录次数。
- [x] 记录运行结果。若已有用例覆盖目标，记录用例及相关断言。

```sh
go test -race -timeout 60s ./x/observability/ops/healthhttp
make website-sync
make website-check-generated
```

## 实施证据

- [TestAcceptanceRetryDelayReturnsContextCauseAndCountsAttempts](../../../x/observability/ops/healthhttp/component_regression_test.go#L126)

[批次验证与基准](../review-a074eadb/README.md)
