# Card 2060 — 门面默认配置多次读取独立

Milestone: 稳定模块第 9 节复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P3
State: done
Primary Module: .
Owned Files: `plumego_test.go`
Depends On: 无

Goal: 门面默认配置多次读取独立
Scope: 取两次门面配置，改动一份端口或 TLS 标志并构造应用。
Non-goals: 范围限于本卡涉及的契约，保持公开签名和依赖。
Files: `plumego_test.go`。
Tests: 另一份和门面的新默认值继续与 core 默认一致。
Docs Sync: 核对新增验收与已有模块说明一致，记录证据和限制。
Done Definition: 完成以下验收，记录命令、结果与剩余限制。
Outcome: 两份门面配置独立，用改过的配置构造的 server 采用新地址，门面默认仍与 core 对齐。

## 证据与影响

证据状态为补证。第 9 节检查号为 3,6,14,36。 [质量规则第 9 节](../../../docs/operations/agent-code-quality-rules.md#L267)

门面配置函数映射到 core。需要在门面层核验修改一种默认配置不会污染后续应用。 [plumego.go:38](../../../plumego.go#L38)

[复审报告](../../../docs/evidence/stable-quality-review-c3a77410.md)

## 验收与验证

- [x] 另一份和门面的新默认值继续与 core 默认一致。
- [x] 已有测试覆盖相同触发和断言时可直接复用，记录具体用例。

```sh
go test -race -timeout 60s .
```

## 实施证据

- [TestSeparateFacadeDefaultConfigsRemainIndependent](../../../plumego_test.go#L270)
