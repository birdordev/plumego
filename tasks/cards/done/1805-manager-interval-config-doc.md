# Card 1805 — 健康间隔配置缺少未启用调度的说明

Milestone: 稳定模块质量复审
Recipe: specs/change-recipes/fix-bug.yaml
Context Package: implementation
Priority: P3
State: done
Primary Module: x/observability
Owned Files: `x/observability/ops/healthhttp/manager.go`, `docs/modules/x/observability/ops/README.md`, `x/observability/ops/healthhttp/manager_test.go`
Depends On: 无

Goal: 给出实际检查入口和配置所有者，保持现有序列化字段。
Scope: 核对调用方后记录该字段的兼容用途及检查触发方。
Non-goals: 维持稳定公开签名与标准库依赖，将验证限于本卡范围。
Files: `x/observability/ops/healthhttp/manager.go`, `docs/modules/x/observability/ops/README.md`, `x/observability/ops/healthhttp/manager_test.go`。
Tests: 给出实际检查入口和配置所有者，保持现有序列化字段。
Docs Sync: 复核模块说明并验证生成数据。
Done Definition: 完成下列验收，记录验证命令、状态及剩余限制。
Outcome: 配置保留间隔值，模块说明明确周期调度及触发操作由调用方拥有。

## 证据与影响

第 9 节检查号为 6,14,16,42,52。证据状态为源码确认。

Config 暴露 HealthCheckInterval，Manager 中没有消费此字段的周期调度。设置该字段不等于自动检查，容易误判缓存新鲜度。 [x/observability/ops/healthhttp/manager.go:29](../../../x/observability/ops/healthhttp/manager.go#L29)

[复审报告](../../../docs/evidence/stable-quality-review-a074eadb.md)

## 验收与验证

- [x] 给出实际检查入口和配置所有者，保持现有序列化字段。
- [x] 记录运行结果。若已有用例覆盖目标，记录用例及相关断言。

```sh
go test -race -timeout 60s ./x/observability/ops/healthhttp
make website-sync
make website-check-generated
```

## 实施证据

- [TestManagerPreservesSchedulingIntervalForCaller](../../../x/observability/ops/healthhttp/manager_test.go#L235)

[批次验证与基准](../review-a074eadb/README.md)
