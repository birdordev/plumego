# Card 1788 — 补证自定义健康宿主 typed nil 注入行为

Milestone: 稳定模块质量复审
Recipe: specs/change-recipes/analysis-only.yaml
Context Package: review
Priority: P2
State: done
Primary Module: x/observability
Owned Files: `x/observability/ops/healthhttp/handlers_test.go`
Depends On: 无

Goal: 已支持入口仍返回 503，兼容决策记录后才改变。
Scope: 对照可安全处理 nil receiver 与 nil 接口，明确构造者责任。
Non-goals: 维持稳定公开签名与标准库依赖，将验证限于本卡范围。
Files: `x/observability/ops/healthhttp/handlers_test.go`。
Tests: 已支持入口仍返回 503，兼容决策记录后才改变。
Docs Sync: 复核本卡涉及的已公开约定。
Done Definition: 完成下列验收，记录验证命令、状态及剩余限制。
Outcome: 空接口返回规范 503，可自行处理 nil receiver 的 Manager 可以产生自己的 503 就绪快照。保持依赖注入约定。

## 证据与影响

第 9 节检查号为 5,15,36,42。证据状态为补证。

接口 nil 的处理确定，动态值为 typed nil 的 Manager 可能是可用零状态或无效配置。 [x/observability/ops/healthhttp/helpers.go:43](../../../x/observability/ops/healthhttp/helpers.go#L43)

[复审报告](../../../docs/evidence/stable-quality-review-d224acbc.md)

## 验收与验证

- [x] 已支持入口仍返回 503，兼容决策记录后才改变。
- [x] 记录运行结果。若已有用例覆盖目标，记录用例及相关断言。

```sh
go test -race -timeout 60s ./x/observability/ops/healthhttp
```

## 实施证据

- [TestReadinessDistinguishesNilInterfaceAndNilSafeManager](../../../x/observability/ops/healthhttp/handlers_test.go#L685)

[批次验证与基准](../review-d224acbc/README.md)
