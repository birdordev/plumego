# Card 2008 — 空 HEAD 响应自动声明零长度

Milestone: 稳定模块第 9 节复审
Recipe: specs/change-recipes/fix-bug.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: router
Owned Files: `router/dispatch.go`, `router/head_regression_test.go`, `docs/modules/router/README.md`
Depends On: 无

Goal: 空 HEAD 响应自动声明零长度
Scope: 为 HEAD 的自动长度推断增加实际写入正字节条件，保留显式 Content-Length、正常小响应推断和现有无正文状态规则。
Non-goals: 范围限于本卡涉及的契约，保持公开签名和依赖。
Files: `router/dispatch.go`, `router/head_regression_test.go`, `docs/modules/router/README.md`。
Tests: 并排跑原生与 Router 的显式及隐式空 HEAD，断言状态、头和空正文，再验证写入正字节的路径。
Docs Sync: 已同步[所属模块说明](../../../docs/modules/router/README.md)，网站生成数据核对通过。
Done Definition: 完成以下验收，记录命令、结果与剩余限制。
Outcome: 已修复。真实回环 HEAD 未写正文时留空自动长度，正字节的小响应仍与原生对齐。

## 证据与影响

证据状态为已复现。第 9 节检查号为 6,17,24,37,45。 [质量规则第 9 节](../../../docs/operations/agent-code-quality-rules.md#L267)

在回环 HTTP 服务中，显式返回 202 且没有写响应体的处理器经 Router 后收到 Content-Length: 0，直接交给标准库时该头缺失。零可能被客户端解释为对应表示确知为空。 [router/dispatch.go:91](../../../router/dispatch.go#L91)

[复审报告](../../../docs/evidence/stable-quality-review-c3a77410.md)

## 验收与验证

- [x] 并排跑原生与 Router 的显式及隐式空 HEAD，断言状态、头和空正文，再验证写入正字节的路径。
- [x] 已有测试覆盖相同触发和断言时可直接复用，记录具体用例。

```sh
go test -race -timeout 60s ./router
```

## 实施证据

- [TestHeadEmptyHandlerKeepsLengthUnspecified](../../../router/head_regression_test.go#L572)
- [TestHeadPreservesImplicitCommit](../../../router/head_regression_test.go#L67)

同一回归在 c3a77410 的原实现上失败，修复后通过。
