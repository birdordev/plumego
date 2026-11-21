# Card 2077 — 通配反查编码普通百分号与非 ASCII

Milestone: 稳定模块第 9 节复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P3
State: done
Primary Module: router
Owned Files: `router/reverse_routing_group_test.go`
Depends On: 无

Goal: 通配反查编码普通百分号与非 ASCII
Scope: 反查后构造真实请求，读取通配参数并对比原名。
Non-goals: 范围限于本卡涉及的契约，保持公开签名和依赖。
Files: `router/reverse_routing_group_test.go`。
Tests: 保留两个字面字符和分段，既有未知键分类保持。
Docs Sync: 核对新增验收与已有模块说明一致，记录证据和限制。
Done Definition: 完成以下验收，记录命令、结果与剩余限制。
Outcome: 复用具名通配反查回归。非 ASCII 和字面百分号路径完成两次分派并保留参数值。

## 证据与影响

证据状态为补证。第 9 节检查号为 6,18,37,45。 [质量规则第 9 节](../../../docs/operations/agent-code-quality-rules.md#L267)

反查逐段 escape 并保留路径分隔符。针对包含字面百分号和非 ASCII 的常规名称补往返证据。 [router/metadata.go:113](../../../router/metadata.go#L113)

[复审报告](../../../docs/evidence/stable-quality-review-2f962d21.md)

## 验收与验证

- [x] 保留两个字面字符和分段，既有未知键分类保持。
- [x] 已有测试覆盖相同触发和断言时可直接复用，记录具体用例。

```sh
go test -race -timeout 60s ./router
```

## 实施证据

- [TestWildcardURLRoundTripsEscapedSuffixFromNestedGroup](../../../router/reverse_routing_group_test.go#L451)
- [TestNamedGroupURLRejectsUnpairedParameterThenRecovers](../../../router/reverse_routing_group_test.go#L481)
