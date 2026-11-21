# Card 1954 — 单组件跟踪把被更新结果覆盖的成功计成失败

Milestone: 稳定模块质量复审
Recipe: specs/change-recipes/fix-bug.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: x/observability/ops
Owned Files: `x/observability/ops/healthhttp/tracker.go`, `x/observability/ops/healthhttp/metrics_test.go`, `docs/modules/x/observability/ops/README.md`
Depends On: 1953

Goal: 分别覆盖较旧回调成功和失败、未开始时不计数，以及外部 Manager 回退。缓存仍由获准发布的调用决定。
Scope: 像批次路径一样，内置 manager 用私有单次执行结果更新统计，保持公开返回值和外部 Manager 的快照回退，并让缓存继续遵循版本规则。
Non-goals: 维持稳定公开签名与标准库依赖，将验证限于本卡范围。
Files: `x/observability/ops/healthhttp/tracker.go`, `x/observability/ops/healthhttp/metrics_test.go`, `docs/modules/x/observability/ops/README.md`。
Tests: 分别覆盖较旧回调成功和失败、未开始时不计数，以及外部 Manager 回退。缓存仍由获准发布的调用决定。
Docs Sync: 行为变更后同步清单对应的模块说明并校验生成数据。
Done Definition: 完成下列验收，记录验证命令、状态及剩余限制。
Outcome: 内置 Tracker 直接计量本次执行。两种成功与失败次序均得到一成一败，零次尝试不计入，发布错误继续返回。

## 证据与影响

第 9 节检查号为 6,31,42,44,46,47，当前证据状态为已复现。 [质量规则第 9 节](../../../docs/operations/agent-code-quality-rules.md#L267)

交错执行的两个回调分别成功与失败，Tracker 最终统计为 0 成功、2 失败。共享快照与返回错误描述最新发布和发布权，无法代表旧调用的回调结果。 [x/observability/ops/healthhttp/tracker.go:241](../../../x/observability/ops/healthhttp/tracker.go#L241)

[复审报告](../../../docs/evidence/stable-quality-review-999d5e4d.md)

## 验收与验证

- [x] 分别覆盖较旧回调成功和失败、未开始时不计数，以及外部 Manager 回退。缓存仍由获准发布的调用决定。
- [x] 记录挂载入口、具体断言和结果。已有测试能覆盖时复用并注明。

```sh
go test -race -timeout 60s ./x/observability/ops/healthhttp
make website-sync
make website-check-generated
```

## 实施证据

- [TestTrackerDirectMetricsUseOwnAttempt](../../../x/observability/ops/healthhttp/metrics_test.go#L430)
- [TestTrackerExternalManagerDirectSnapshotFallback](../../../x/observability/ops/healthhttp/metrics_test.go#L515)

所属回归在 999d5e4d 的生产代码 overlay 上如预期失败，修改后连续十次竞态运行通过。
