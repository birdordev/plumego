# Card 2047 — 缓存 Append 对输入和返回切片隔离

Milestone: 稳定模块第 9 节复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: store/cache
Owned Files: `store/cache/state_regression_test.go`
Depends On: 无

Goal: 缓存 Append 对输入和返回切片隔离
Scope: Append 普通字节后改写输入并改写第一次 Get 的输出。
Non-goals: 范围限于本卡涉及的契约，保持公开签名和依赖。
Files: `store/cache/state_regression_test.go`。
Tests: 下一次 Get 仍为原合并值，尺寸统计不受外部写影响。
Docs Sync: 核对新增验收与已有模块说明一致，记录证据和限制。
Done Definition: 完成以下验收，记录命令、结果与剩余限制。
Outcome: Append 之后复用输入和读取切片均不影响缓存值，计量保持 3 字节。

## 证据与影响

证据状态为补证。第 9 节检查号为 32,34,41,45。 [质量规则第 9 节](../../../docs/operations/agent-code-quality-rules.md#L267)

Append 与容量计量和已有内容共享同一写阶段。需要调用方缓冲区复用证据。 [store/cache/memory.go:392](../../../store/cache/memory.go#L392)

[复审报告](../../../docs/evidence/stable-quality-review-c3a77410.md)

## 验收与验证

- [x] 下一次 Get 仍为原合并值，尺寸统计不受外部写影响。
- [x] 已有测试覆盖相同触发和断言时可直接复用，记录具体用例。

```sh
go test -race -timeout 60s ./store/cache
```

## 实施证据

- [TestAppendKeepsIndependentInputAndReadSnapshots](../../../store/cache/state_regression_test.go#L617)
