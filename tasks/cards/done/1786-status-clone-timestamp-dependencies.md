# Card 1786 — 核对 HealthStatus.Clone 的时间、依赖和浅层详情归属

Milestone: 稳定模块质量复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: health
Owned Files: `health/health_test.go`
Depends On: 无

Goal: 模型状态及时间不变，依赖切片独立。
Scope: 验证顶层复制与时间偏移，修改依赖副本后对照 JSON。
Non-goals: 维持稳定公开签名与标准库依赖，将验证限于本卡范围。
Files: `health/health_test.go`。
Tests: 模型状态及时间不变，依赖切片独立。
Docs Sync: 复核本卡涉及的已公开约定。
Done Definition: 完成下列验收，记录验证命令、状态及剩余限制。
Outcome: HealthStatus.Clone 保留状态及带偏移时间，复制依赖和顶层详情，嵌套 details 依照浅复制契约。

## 证据与影响

第 9 节检查号为 16,34,42。证据状态为补证。

前轮看了 Readiness 时间及 Component 的启用标志；HealthStatus 自身还能带时间、依赖和任意 details。 [health/core.go:45](../../../health/core.go#L45)

[复审报告](../../../docs/evidence/stable-quality-review-d224acbc.md)

## 验收与验证

- [x] 模型状态及时间不变，依赖切片独立。
- [x] 记录运行结果。若已有用例覆盖目标，记录用例及相关断言。

```sh
go test -race -timeout 60s ./health
```

## 实施证据

- [TestHealthStatusCloneKeepsTimestampAndSeparatesDependencies](../../../health/health_test.go#L166)

[批次验证与基准](../review-d224acbc/README.md)
