# Card 1837 — 核对幂等键输入规范和调用方一致性

Milestone: 稳定模块质量复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: store
Owned Files: `store/idempotency/store_test.go`
Depends On: 无

Goal: 固定错误可识别，保留已有合法键。
Scope: 用合成 Unicode 与边界空格仅核对稳定校验，不增加归一化策略。
Non-goals: 维持稳定公开签名与标准库依赖，将验证限于本卡范围。
Files: `store/idempotency/store_test.go`。
Tests: 固定错误可识别，保留已有合法键。
Docs Sync: 复核本卡涉及的已公开约定。
Done Definition: 完成下列验收，记录验证命令、状态及剩余限制。
Outcome: 带边界空格与 Unicode 的非空键合法，空白键及控制字符返回稳定错误。

## 证据与影响

第 9 节检查号为 6,18,22,41。证据状态为补证。

函数用 TrimSpace 检查空键并拒绝控制字符，原字符串的等价性由后端决定。 [store/idempotency/store.go:100](../../../store/idempotency/store.go#L100)

[复审报告](../../../docs/evidence/stable-quality-review-a074eadb.md)

## 验收与验证

- [x] 固定错误可识别，保留已有合法键。
- [x] 记录运行结果。若已有用例覆盖目标，记录用例及相关断言。

```sh
go test -race -timeout 60s ./store/idempotency
```

## 实施证据

- [TestValidateKeyKeepsLegalUnicodeAndBoundarySpaces](../../../store/idempotency/store_test.go#L353)

[批次验证与基准](../review-a074eadb/README.md)
