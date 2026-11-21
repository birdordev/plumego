# Card 2109 — JSON 具名无符号整数与空指针混合字段

Milestone: 稳定模块第 9 节复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P3
State: done
Primary Module: log
Owned Files: `log/json_test.go`
Depends On: 无

Goal: JSON 具名无符号整数与空指针混合字段
Scope: 通过公开 logger 写一条结构化记录，用 UseNumber 解析。
Non-goals: 范围限于本卡涉及的契约，保持公开签名和依赖。
Files: `log/json_test.go`。
Tests: 整数十进制精确，空指针字段保持预期值并带一条合法 JSON 记录。
Docs Sync: 核对新增验收与已有模块说明一致，记录证据和限制。
Done Definition: 完成以下验收，记录命令、结果与剩余限制。
Outcome: 一条 JSON 同时保留具名 uint64 最大值的十进制精度和显式 null，解码后没有余下记录。

## 证据与影响

证据状态为补证。第 9 节检查号为 6,34,43,45。 [质量规则第 9 节](../../../docs/operations/agent-code-quality-rules.md#L267)

规范器分流类型化值与自定义编码。用无符号整数边界和普通空指针观察默认编码精度。 [log/json.go:209](../../../log/json.go#L209)

[复审报告](../../../docs/evidence/stable-quality-review-2f962d21.md)

## 验收与验证

- [x] 整数十进制精确，空指针字段保持预期值并带一条合法 JSON 记录。
- [x] 已有测试覆盖相同触发和断言时可直接复用，记录具体用例。

```sh
go test -race -timeout 60s ./log
```

## 实施证据

- [TestJSONNamedUintAndTypedNilShareAValidRecord](../../../log/json_test.go#L1042)
