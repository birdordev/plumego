# Card 2080 — 错误详情中具名字节切片的快照

Milestone: 稳定模块第 9 节复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: contract
Owned Files: `contract/errors_chain_test.go`
Depends On: 无

Goal: 错误详情中具名字节切片的快照
Scope: 写入具名普通字节并分别改写原数据和返回数据，输出 HTTP 错误。
Non-goals: 范围限于本卡涉及的契约，保持公开签名和依赖。
Files: `contract/errors_chain_test.go`。
Tests: 错误链分类保持，结果 JSON 对应原始值，克隆保留类型。
Docs Sync: 核对新增验收与已有模块说明一致，记录证据和限制。
Done Definition: 完成以下验收，记录命令、结果与剩余限制。
Outcome: 具名字节切片在 Builder 与 Details 两端独立复制，编码保留原数据，错误原因仍可分类。

## 证据与影响

证据状态为补证。第 9 节检查号为 22,34,38,45。 [质量规则第 9 节](../../../docs/operations/agent-code-quality-rules.md#L267)

反射分支涉及带类型名的切片。用可编码的具名 []byte 检查 Builder 输入和 Details 输出隔离。 [contract/errors.go:612](../../../contract/errors.go#L612)

[复审报告](../../../docs/evidence/stable-quality-review-2f962d21.md)

## 验收与验证

- [x] 错误链分类保持，结果 JSON 对应原始值，克隆保留类型。
- [x] 已有测试覆盖相同触发和断言时可直接复用，记录具体用例。

```sh
go test -race -timeout 60s ./contract
```

## 实施证据

- [TestErrorDetailsOwnNamedByteSlices](../../../contract/errors_chain_test.go#L303)
