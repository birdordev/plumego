# Card 1815 — 核对复用 ErrorBuilder 的多次构建快照

Milestone: 稳定模块质量复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: contract
Owned Files: `contract/errors_test.go`
Depends On: 无

Goal: 先前对象和 JSON 不变，新对象反映新值。
Scope: 同一 builder 构造两份普通详情再分别写出。
Non-goals: 维持稳定公开签名与标准库依赖，将验证限于本卡范围。
Files: `contract/errors_test.go`。
Tests: 先前对象和 JSON 不变，新对象反映新值。
Docs Sync: 复核本卡涉及的已公开约定。
Done Definition: 完成下列验收，记录验证命令、状态及剩余限制。
Outcome: 复用 ErrorBuilder 生成的后续详情不修改早先快照及 JSON 形状。

## 证据与影响

第 9 节检查号为 6,12,34,38。证据状态为补证。

每次 Build 归一化内部错误，支持容器复制。待核对修改 builder 与源数据时的相邻构建。 [contract/errors.go:353](../../../contract/errors.go#L353)

[复审报告](../../../docs/evidence/stable-quality-review-a074eadb.md)

## 验收与验证

- [x] 先前对象和 JSON 不变，新对象反映新值。
- [x] 记录运行结果。若已有用例覆盖目标，记录用例及相关断言。

```sh
go test -race -timeout 60s ./contract
```

## 实施证据

- [TestErrorBuilderReuseKeepsPreviousDetailsAndWireShape](../../../contract/errors_test.go#L1237)

[批次验证与基准](../review-a074eadb/README.md)
