# Card 1957 — 健康 Manager 的 Enabled 字段缺少兼容语义说明

Milestone: 稳定模块质量复审
Recipe: specs/change-recipes/fix-bug.yaml
Context Package: implementation
Priority: P3
State: done
Primary Module: x/observability/ops
Owned Files: `x/observability/ops/healthhttp/manager.go`, `x/observability/ops/healthhttp/manager_test.go`, `docs/modules/x/observability/ops/README.md`
Depends On: 无

Goal: 公开说明和 true、false、Config{} 三组观察一致，JSON 字段兼容并通过网站生成校验。
Scope: 补齐字段注释与宿主文档，说明显式调用仍执行，以及需要停用时由装配方控制是否挂载或调度。保持零值调用兼容。
Non-goals: 维持稳定公开签名与标准库依赖，将验证限于本卡范围。
Files: `x/observability/ops/healthhttp/manager.go`, `x/observability/ops/healthhttp/manager_test.go`, `docs/modules/x/observability/ops/README.md`。
Tests: 公开说明和 true、false、Config{} 三组观察一致，JSON 字段兼容并通过网站生成校验。
Docs Sync: 行为变更后同步清单对应的模块说明并校验生成数据。
Done Definition: 完成下列验收，记录验证命令、状态及剩余限制。
Outcome: 公开注释和模块说明校对完毕，三组配置均执行显式检查且 JSON 字段仍可读。网站重新生成未产生差异。

## 证据与影响

第 9 节检查号为 6,14,16,42,52，当前证据状态为已复现。 [质量规则第 9 节](../../../docs/operations/agent-code-quality-rules.md#L267)

Config.Enabled 可以设为 true 或 false，两个构造配置都会执行显式检查。构造保存字段，Manager 未用它控制检查，现有字段也没有解释。 [x/observability/ops/healthhttp/manager.go:25](../../../x/observability/ops/healthhttp/manager.go#L25)

[复审报告](../../../docs/evidence/stable-quality-review-999d5e4d.md)

## 验收与验证

- [x] 公开说明和 true、false、Config{} 三组观察一致，JSON 字段兼容并通过网站生成校验。
- [x] 记录挂载入口、具体断言和结果。已有测试能覆盖时复用并注明。

```sh
go test -race -timeout 60s ./x/observability/ops/healthhttp
make website-sync
make website-check-generated
```

## 实施证据

- [TestManagerEnabledFieldPreservesExplicitChecks](../../../x/observability/ops/healthhttp/manager_test.go#L256)
