# Card 2101 — 非整数 Incr failure 后容量可复用

Milestone: 稳定模块第 9 节复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: store/cache
Owned Files: `store/cache/state_regression_test.go`
Depends On: 无

Goal: 非整数 Incr failure 后容量可复用
Scope: 存入不可解析的普通短值，命中容量上限后 Incr，再等原期限前后各读取。
Non-goals: 范围限于本卡涉及的契约，保持公开签名和依赖。
Files: `store/cache/state_regression_test.go`。
Tests: 错误分类准确，原值和期限保留，容量在合法清理后可用。
Docs Sync: 核对新增验收与已有模块说明一致，记录证据和限制。
Done Definition: 完成以下验收，记录命令、结果与剩余限制。
Outcome: 解析失败保留原字节和容量占用，精确过期后读取清理并可复用容量。

## 证据与影响

证据状态为补证。第 9 节检查号为 22,31,35,41,46。 [质量规则第 9 节](../../../docs/operations/agent-code-quality-rules.md#L267)

解码失败在写前返回，需与精确预算和原过期时点同时观察。 [store/cache/memory.go:343](../../../store/cache/memory.go#L343)

[复审报告](../../../docs/evidence/stable-quality-review-2f962d21.md)

## 验收与验证

- [x] 错误分类准确，原值和期限保留，容量在合法清理后可用。
- [x] 已有测试覆盖相同触发和断言时可直接复用，记录具体用例。

```sh
go test -race -timeout 60s ./store/cache
```

## 实施证据

- [TestNonIntegerIncrPreservesExpiryAndCapacity](../../../store/cache/state_regression_test.go#L663)
