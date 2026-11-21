# Card 1800 — 最大 RetryCount 被溢出成单次检查

Milestone: 稳定模块质量复审
Recipe: specs/change-recipes/fix-bug.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: x/observability
Owned Files: `x/observability/ops/healthhttp/manager.go`, `x/observability/ops/healthhttp/manager_test.go`, `docs/modules/x/observability/ops/README.md`
Depends On: 无

Goal: 构造和 SetConfig 处理一致，普通 0、1 重试仍符合约定，负值继续拒绝。
Scope: 定义可表达的总尝试次数，在公共配置验证边界拒绝不可表达值或以不溢出的边界计算。
Non-goals: 维持稳定公开签名与标准库依赖，将验证限于本卡范围。
Files: `x/observability/ops/healthhttp/manager.go`, `x/observability/ops/healthhttp/manager_test.go`, `docs/modules/x/observability/ops/README.md`。
Tests: 构造和 SetConfig 处理一致，普通 0、1 重试仍符合约定，负值继续拒绝。
Docs Sync: 复核模块说明并验证生成数据。
Done Definition: 完成下列验收，记录验证命令、状态及剩余限制。
Outcome: 构造和 SetConfig 一致拒绝不可表达的尝试数，普通重试次数保持原契约。

## 证据与影响

第 9 节检查号为 14,22,35,42。证据状态为已复现。

构造接受最大 int。受控检查第一次失败、第二次本可成功，实际仅调用一次，配置被静默改变。 [x/observability/ops/healthhttp/manager.go:464](../../../x/observability/ops/healthhttp/manager.go#L464)

[复审报告](../../../docs/evidence/stable-quality-review-a074eadb.md)

## 验收与验证

- [x] 构造和 SetConfig 处理一致，普通 0、1 重试仍符合约定，负值继续拒绝。
- [x] 记录运行结果。若已有用例覆盖目标，记录用例及相关断言。

```sh
go test -race -timeout 60s ./x/observability/ops/healthhttp
make website-sync
make website-check-generated
```

## 实施证据

- [TestAcceptanceRetryCountRejectsUnrepresentableAttempts](../../../x/observability/ops/healthhttp/manager_test.go#L210)

[批次验证与基准](../review-a074eadb/README.md)
