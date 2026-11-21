# Card 1942 — 核对未知状态随 Clone 和 JSON 编码保留

Milestone: 稳定模块质量复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P3
State: done
Primary Module: health
Owned Files: `health/json_contract_test.go`
Depends On: 无

Goal: 未知状态不会被模型改写为 healthy，只被就绪判定拒绝。
Scope: 构造普通未知状态、空状态和合法 degraded，比较模型 Clone 和 JSON，核对 readiness helper。
Non-goals: 维持稳定公开签名与标准库依赖，将验证限于本卡范围。
Files: `health/json_contract_test.go`。
Tests: 未知状态不会被模型改写为 healthy，只被就绪判定拒绝。
Docs Sync: 记录核验结论；只在现有契约文字需要澄清时另提文档变更。
Done Definition: 完成下列验收，记录验证命令、状态及剩余限制。
Outcome: 未知或零状态由模型保留原字符串与详情，在 IsKnown 和 IsReady 判定中拒绝。

## 证据与影响

第 9 节检查号为 6,16,34,38,42。证据状态为补证。

HealthState 是字符串值，未知项不就绪。模型编码是否改写未知状态由调用端政策独立于模型。 [health/core.go:68](../../../health/core.go#L68)

[复审报告](../../../docs/evidence/stable-quality-review-2b94cf86.md)

## 验收与验证

- [x] 未知状态不会被模型改写为 healthy，只被就绪判定拒绝。
- [x] 记录挂载入口、具体断言和结果。已有测试能覆盖时复用并注明。

```sh
go test -race -timeout 60s ./health
```

## 实施证据

- [TestStatusModelPreservesUnknownAndDetailsWhileRejectingReadiness](../../../health/json_contract_test.go#L287)

[批次验证与基准](../review-2b94cf86/README.md)
