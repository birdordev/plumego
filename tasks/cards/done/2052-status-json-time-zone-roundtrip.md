# Card 2052 — 健康时间戳时区及瞬时值往返

Milestone: 稳定模块第 9 节复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P3
State: done
Primary Module: health
Owned Files: `health/json_contract_test.go`
Depends On: 无

Goal: 健康时间戳时区及瞬时值往返
Scope: 写入固定区时间戳并 JSON 往返。
Non-goals: 范围限于本卡涉及的契约，保持公开签名和依赖。
Files: `health/json_contract_test.go`。
Tests: 对比实际瞬时值、偏移及公开字段名，保留合法表示。
Docs Sync: 核对新增验收与已有模块说明一致，记录证据和限制。
Done Definition: 完成以下验收，记录命令、结果与剩余限制。
Outcome: 含固定非 UTC 偏移的健康时间戳 JSON 往返保持纳秒与偏移。

## 证据与影响

证据状态为补证。第 9 节检查号为 6,16,38,42。 [质量规则第 9 节](../../../docs/operations/agent-code-quality-rules.md#L267)

健康模型直接采用 time.Time 编码，需检查常规非 UTC 偏移的表示与 instant。 [health/core.go:32](../../../health/core.go#L32)

[复审报告](../../../docs/evidence/stable-quality-review-c3a77410.md)

## 验收与验证

- [x] 对比实际瞬时值、偏移及公开字段名，保留合法表示。
- [x] 已有测试覆盖相同触发和断言时可直接复用，记录具体用例。

```sh
go test -race -timeout 60s ./health
```

## 实施证据

- [TestHealthTimestampWithOffsetRoundTrips](../../../health/json_contract_test.go#L337)
