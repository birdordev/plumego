# Card 2091 — BodyLimit 精确字节并带 EOF 的输入

Milestone: 稳定模块第 9 节复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: middleware/bodylimit
Owned Files: `middleware/bodylimit/body_limit_test.go`
Depends On: 无

Goal: BodyLimit 精确字节并带 EOF 的输入
Scope: 使用普通短长度 read stub，比较正好等于限额和短于限额两组。
Non-goals: 范围限于本卡涉及的契约，保持公开签名和依赖。
Files: `middleware/bodylimit/body_limit_test.go`。
Tests: 数据完整，next 成功，无超额日志或额外状态。
Docs Sync: 核对新增验收与已有模块说明一致，记录证据和限制。
Done Definition: 完成以下验收，记录命令、结果与剩余限制。
Outcome: 普通 reader 同批返回最终字节与 EOF。正好限额和短于限额时均保留全部字节及 204。

## 证据与影响

证据状态为补证。第 9 节检查号为 20,24,35,39,46。 [质量规则第 9 节](../../../docs/operations/agent-code-quality-rules.md#L267)

底层 Read 可同批返回最后字节与 EOF，限额相等时不应触发过量。 [middleware/bodylimit/body_limit.go:182](../../../middleware/bodylimit/body_limit.go#L182)

[复审报告](../../../docs/evidence/stable-quality-review-2f962d21.md)

## 验收与验证

- [x] 数据完整，next 成功，无超额日志或额外状态。
- [x] 已有测试覆盖相同触发和断言时可直接复用，记录具体用例。

```sh
go test -race -timeout 60s ./middleware/bodylimit
```

## 实施证据

- [TestBodyLimitRetainsFinalBytesReturnedWithEOF](../../../middleware/bodylimit/body_limit_test.go#L907)
