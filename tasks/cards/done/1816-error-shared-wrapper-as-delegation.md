# Card 1816 — 核对自定义 As 回调与值优先级

Milestone: 稳定模块质量复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: contract
Owned Files: `contract/errors_chain_test.go`
Depends On: 无

Goal: 值优先契约成立，保留直接错误链。
Scope: 用有界合成 wrapper 比较指针候选和后续值候选。
Non-goals: 维持稳定公开签名与标准库依赖，将验证限于本卡范围。
Files: `contract/errors_chain_test.go`。
Tests: 值优先契约成立，保留直接错误链。
Docs Sync: 复核本卡涉及的已公开约定。
Done Definition: 完成下列验收，记录验证命令、状态及剩余限制。
Outcome: 委托指针兼容值作为后备，后续直接值优先。

## 证据与影响

第 9 节检查号为 13,22,33,38。证据状态为补证。

错误链遍历能调用自定义 As，先前聚焦 typed nil 和共享枝。需锁定合法代理回调下的优先级。 [contract/errors.go:383](../../../contract/errors.go#L383)

[复审报告](../../../docs/evidence/stable-quality-review-a074eadb.md)

## 验收与验证

- [x] 值优先契约成立，保留直接错误链。
- [x] 记录运行结果。若已有用例覆盖目标，记录用例及相关断言。

```sh
go test -race -timeout 60s ./contract
```

## 实施证据

- [TestAsDelegatedPointerIsFallbackToLaterValue](../../../contract/errors_chain_test.go#L249)

[批次验证与基准](../review-a074eadb/README.md)
