# Card 1757 — 核对 HEAD 逻辑状态与连接接管顺序

Milestone: 稳定模块质量复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: router
Owned Files: `router/head_regression_test.go`
Depends On: 无

Goal: 成功后不写 HTTP 状态或正文，拒绝时保留合法终态。
Scope: 用计数型 writer mock 核对已选状态、可接管能力和失败回退。
Non-goals: 维持稳定公开签名与标准库依赖，将验证限于本卡范围。
Files: `router/head_regression_test.go`。
Tests: 成功后不写 HTTP 状态或正文，拒绝时保留合法终态。
Docs Sync: 复核本卡涉及的已公开约定。
Done Definition: 完成下列验收，记录验证命令、状态及剩余限制。
Outcome: HEAD 已选状态后成功接管时不发送 HTTP 状态或正文，失败时保持原选状态和推断长度。

## 证据与影响

第 9 节检查号为 23,24,37。证据状态为补证。

HEAD 在返回前延迟提交，成功接管和拒绝接管的归属需和普通状态路径对照。 [router/dispatch.go:147](../../../router/dispatch.go#L147)

[复审报告](../../../docs/evidence/stable-quality-review-d224acbc.md)

## 验收与验证

- [x] 成功后不写 HTTP 状态或正文，拒绝时保留合法终态。
- [x] 记录运行结果。若已有用例覆盖目标，记录用例及相关断言。

```sh
go test -race -timeout 60s ./router
```

## 实施证据

- [TestHeadHijackRejectsWritesWithoutCommittingHeaders](../../../router/head_regression_test.go#L254)
- [TestHeadFailedHijackPreservesResponse](../../../router/head_regression_test.go#L275)

[批次验证与基准](../review-d224acbc/README.md)
