# Card 2105 — 就绪模型 Clone 的显式空 map 独立性

Milestone: 稳定模块第 9 节复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P3
State: done
Primary Module: health
Owned Files: `health/health_test.go`
Depends On: 无

Goal: 就绪模型 Clone 的显式空 map 独立性
Scope: 并排检验 nil、显式空与单值 map 后对克隆写入。
Non-goals: 范围限于本卡涉及的契约，保持公开签名和依赖。
Files: `health/health_test.go`。
Tests: 内存形状与 JSON 互不混淆，修改副本不影响原对象。
Docs Sync: 核对新增验收与已有模块说明一致，记录证据和限制。
Done Definition: 完成以下验收，记录命令、结果与剩余限制。
Outcome: 复用模型回归。nil、显式空和含 false 项的 map 样例保持独立 Clone 与 JSON 可见形状。

## 证据与影响

证据状态为补证。第 9 节检查号为 6,16,34,42。 [质量规则第 9 节](../../../docs/operations/agent-code-quality-rules.md#L267)

Clone 保留 map 的初始化形状，JSON 省略空 map。 [health/readiness.go:18](../../../health/readiness.go#L18)

[复审报告](../../../docs/evidence/stable-quality-review-2f962d21.md)

## 验收与验证

- [x] 内存形状与 JSON 互不混淆，修改副本不影响原对象。
- [x] 已有测试覆盖相同触发和断言时可直接复用，记录具体用例。

```sh
go test -race -timeout 60s ./health
```

## 实施证据

- [TestReadinessClonesKeepNilDistinctFromWritableEmptyMap](../../../health/health_test.go#L203)
- [TestReadinessEmptyComponentsWireAndClone](../../../health/json_contract_test.go#L187)
