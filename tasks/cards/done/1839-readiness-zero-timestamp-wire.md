# Card 1839 — 核对零值就绪记录的时间和 JSON 字段

Milestone: 稳定模块质量复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P3
State: done
Primary Module: health
Owned Files: `health/json_contract_test.go`
Depends On: 无

Goal: 不隐式生成时间或状态，字段形状清楚。
Scope: 序列化零值与显式不可就绪对象，检查时间、原因和空组件。
Non-goals: 维持稳定公开签名与标准库依赖，将验证限于本卡范围。
Files: `health/json_contract_test.go`。
Tests: 不隐式生成时间或状态，字段形状清楚。
Docs Sync: 复核本卡涉及的已公开约定。
Done Definition: 完成下列验收，记录验证命令、状态及剩余限制。
Outcome: 序列化保留零时间及 false，按约定省略空原因和空组件。

## 证据与影响

第 9 节检查号为 6,16,38,42。证据状态为补证。

Timestamp 不省略，已有非零偏移测试。零值是否需要应用层替换由调用方所有。 [health/readiness.go:13](../../../health/readiness.go#L13)

[复审报告](../../../docs/evidence/stable-quality-review-a074eadb.md)

## 验收与验证

- [x] 不隐式生成时间或状态，字段形状清楚。
- [x] 记录运行结果。若已有用例覆盖目标，记录用例及相关断言。

```sh
go test -race -timeout 60s ./health
```

## 实施证据

- [TestReadinessZeroValueHasExplicitTimeAndState](../../../health/json_contract_test.go#L258)

[批次验证与基准](../review-a074eadb/README.md)
